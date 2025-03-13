package childgoroutine

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/xtaci/smux"
)

// registry maps names to functions to run in the child process.
var registry = make(map[string]func(string))

// muxSession holds the smux session in child mode.
var muxSession *smux.Session

// Child represents a spawned child process with its process handle and
// a multiplexed smux session.
type Child struct {
	Process *os.Process
	Mux     *smux.Session
}

// Register makes a function available for running in a child process.
// Because the child re‑executes your binary, registration must be done in both
// parent and child.
func Register(name string, f func(string)) {
	registry[name] = f
}

// ---------------------------------------------------------------------
// Parent-Side: Go()
// ---------------------------------------------------------------------

// Go spawns a child process that will run the registered function given by name.
// It creates a full-duplex IPC channel over two pipes and wraps it in a smux session
// (acting as the smux server). The child process will receive its endpoints as
// inherited file descriptors, reassemble its net.Conn, and then wrap it with smux.Client.
func Go(name string, args string) (*Child, error) {
	parentConn, childRead, childWrite, err := createDuplexPipe()
	if err != nil {
		return nil, err
	}
	// Wrap the parent's connection in a smux server session.
	session, err := smux.Server(parentConn, smux.DefaultConfig())
	if err != nil {
		parentConn.Close()
		return nil, fmt.Errorf("failed to create smux server: %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to get executable: %v", err)
	}

	// Pass command-line flags so that the child knows:
	//   • It is running in child mode.
	//   • Which registered function to run.
	//   • The file-descriptor numbers for its IPC channel.
	//
	// The ordering of Files in os.StartProcess causes the inherited files to be
	// assigned to FDs 3 and 4 in the child (after stdin=0, stdout=1, stderr=2).
	cmdArgs := []string{
		"--child",
		"--childName", name,
		"--ipcReadFD", "3",
		"--ipcWriteFD", "4",
		"--args", args,
	}

	// Spawn the child process. Pass in:
	//   [os.Stdin, os.Stdout, os.Stderr, child's read endpoint, child's write endpoint]
	files := []*os.File{os.Stdin, os.Stdout, os.Stderr, childRead, childWrite}

	// Use exec.Command which is well supported on Windows.
	cmd := exec.Command(exe, cmdArgs...)
	cmd.Dir = ""
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Pass the extra files. These will become FDs 3 and 4 in the child process.
	cmd.ExtraFiles = files[3:]

	// Start the child process.
	err = cmd.Start()
	// The parent's copies of the child's pipe endpoints are no longer needed.
	childRead.Close()
	childWrite.Close()

	if err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to start child process: %v", err)
	}

	// Detach from the child process.
	if err := cmd.Process.Release(); err != nil {
		fmt.Fprintf(os.Stderr,
			"warning: failed to release child process: %v\n", err)
	}

	return &Child{
		Process: cmd.Process,
		Mux:     session,
	}, nil
}

// ---------------------------------------------------------------------
// Child-Side: runChildMode()
// ---------------------------------------------------------------------

// SMux returns the smux session in the child process.
// The child’s registered function may use SMux() to open or accept multiplexed streams.
func SMux() *smux.Session {
	return muxSession
}

// runChildMode checks if this process was spawned as a child (via a flag).
// If so, it reassembles the IPC channel from the inherited file descriptors,
// wraps it in a smux client (acting as the client side of the multiplexed session),
// and then executes the function registered under the provided name.
func runChildMode() {
	var childMode bool
	var childName string
	var ipcReadFDStr string
	var ipcWriteFDStr string
	var argsStr string

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--child", "-child":
			childMode = true
		case "--childName", "-childName":
			if i+1 < len(args) {
				childName = args[i+1]
				i++
			}
		case "--ipcReadFD", "-ipcReadFD":
			if i+1 < len(args) {
				ipcReadFDStr = args[i+1]
				i++
			}
		case "--ipcWriteFD", "-ipcWriteFD":
			if i+1 < len(args) {
				ipcWriteFDStr = args[i+1]
				i++
			}
		case "--args", "-args":
			if i+1 < len(args) {
				argsStr = args[i+1]
				i++
			}
		}
	}

	if childMode {
		// If both IPC file descriptor flags are provided, reassemble the IPC connection.
		if ipcReadFDStr != "" && ipcWriteFDStr != "" {
			fdRead, err := strconv.Atoi(ipcReadFDStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid ipcReadFD value: %v\n", err)
				os.Exit(1)
			}
			fdWrite, err := strconv.Atoi(ipcWriteFDStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid ipcWriteFD value: %v\n", err)
				os.Exit(1)
			}
			readFile := os.NewFile(uintptr(fdRead), "child-ipc-read")
			writeFile := os.NewFile(uintptr(fdWrite), "child-ipc-write")
			conn := newPipeDuplex(readFile, writeFile)
			// Wrap the IPC connection in a smux client session.
			mux, err := smux.Client(conn, smux.DefaultConfig())
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to create smux client: %v\n", err)
				os.Exit(1)
			}
			muxSession = mux
		}
		if childName == "" {
			fmt.Fprintln(os.Stderr,
				"child mode specified but no child name provided")
			os.Exit(1)
		}
		f, ok := registry[childName]
		if !ok {
			fmt.Fprintf(os.Stderr,
				"no registered function for child name: %s\n", childName)
			os.Exit(1)
		}
		// Run the registered child function.
		f(argsStr)
		if muxSession != nil {
			muxSession.Close()
		}
		os.Exit(0)
	}
}

func init() {
	runChildMode()
}
