package childgoroutine

import (
	"fmt"
	"os"
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
