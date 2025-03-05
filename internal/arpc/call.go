package arpc

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/sonroyaalmerol/pbs-plus/internal/arpc/arpcdata"
)

// Call initiates a request/response conversation on a new stream.
func (s *Session) Call(method string, payload arpcdata.Encodable) (Response, error) {
	return s.CallContext(context.Background(), method, payload)
}

func (s *Session) CallWithTimeout(timeout time.Duration, method string, payload arpcdata.Encodable) (Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return s.CallContext(ctx, method, payload)
}

// CallContext performs an RPC call over a new stream.
// It applies any context deadlines to the smux stream.
func (s *Session) CallContext(ctx context.Context, method string, payload arpcdata.Encodable) (Response, error) {
	// Grab the current smux session
	curSession := s.muxSess.Load()

	// Open a new stream. (Note: while stream reuse might reduce overhead,
	// with smux the recommended pattern is one stream per RPC call to avoid
	// interleaved messages. If your protocol allows reuse, you might pool streams.)
	stream, err := openStreamWithReconnect(s, curSession)
	if err != nil {
		return Response{}, err
	}
	defer stream.Close()

	// Propagate context deadlines to the stream
	if deadline, ok := ctx.Deadline(); ok {
		stream.SetWriteDeadline(deadline)
		stream.SetReadDeadline(deadline)
	}

	// Serialize the payload if provided
	var payloadBytes []byte
	if payload != nil {
		payloadBytes, err = payload.Encode()
		if err != nil {
			return Response{}, fmt.Errorf("failed to encode payload: %w", err)
		}
	}

	// Build the RPC request and encode it.
	req := Request{
		Method:  method,
		Payload: payloadBytes,
	}
	reqBytes, err := req.Encode()
	if err != nil {
		return Response{}, fmt.Errorf("failed to encode request: %w", err)
	}

	if _, err := stream.Write(reqBytes); err != nil {
		return Response{}, fmt.Errorf("failed to write request: %w", err)
	}

	prefix := make([]byte, 4)
	if _, err := io.ReadFull(stream, prefix); err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return Response{}, context.DeadlineExceeded
		}
		return Response{}, fmt.Errorf("failed to read length prefix: %w", err)
	}
	totalLength := binary.LittleEndian.Uint32(prefix)
	if totalLength < 4 {
		return Response{}, fmt.Errorf("invalid total length %d", totalLength)
	}

	// Allocate a buffer with exactly totalLength bytes.
	buf := make([]byte, totalLength)

	// Copy the already-read prefix into buf.
	copy(buf, prefix)
	// Read the remaining totalLength-4 bytes.
	if _, err := io.ReadFull(stream, buf[4:]); err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return Response{}, context.DeadlineExceeded
		}
		return Response{}, fmt.Errorf("failed to read full response: %w", err)
	}

	// Decode the response.
	var resp Response
	if err := resp.Decode(buf); err != nil {
		return Response{}, fmt.Errorf("failed to decode response: %w", err)
	}

	return resp, nil
}

// CallMsg performs an RPC call and unmarshals its Data into v on success,
// or decodes the error from Data if status != http.StatusOK.
func (s *Session) CallMsg(ctx context.Context, method string, payload arpcdata.Encodable) ([]byte, error) {
	resp, err := s.CallContext(ctx, method, payload)
	if err != nil {
		return nil, err
	}

	// Handle error responses
	if resp.Status != http.StatusOK {
		if resp.Data != nil {
			var serErr SerializableError
			if err := serErr.Decode(resp.Data); err != nil {
				return nil, fmt.Errorf("RPC error: %s (status %d)", resp.Message, resp.Status)
			}
			return nil, UnwrapError(serErr)
		}
		return nil, fmt.Errorf("RPC error: %s (status %d)", resp.Message, resp.Status)
	}

	// Return the response data
	if resp.Data == nil {
		return nil, nil
	}
	return resp.Data, nil
}

func (s *Session) CallMsgWithTimeout(timeout time.Duration, method string, payload arpcdata.Encodable) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return s.CallMsg(ctx, method, payload)
}

// CallMsgWithBuffer performs an RPC call for file I/O-style operations in which the server
// first sends metadata about a binary transfer and then writes the payload directly.
func (s *Session) CallMsgWithBuffer(ctx context.Context, method string, payload arpcdata.Encodable, buffer []byte) (int, error) {
	curSession := s.muxSess.Load()
	stream, err := openStreamWithReconnect(s, curSession)
	if err != nil {
		return 0, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	// Propagate context deadlines to the stream
	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(deadline)
	}

	// Serialize the payload
	var payloadBytes []byte
	if payload != nil {
		payloadBytes, err = payload.Encode()
		if err != nil {
			return 0, fmt.Errorf("failed to encode payload: %w", err)
		}
	}

	// Build the RPC request
	req := Request{
		Method:  method,
		Payload: payloadBytes,
	}

	// Encode and send the request
	reqBytes, err := req.Encode()
	if err != nil {
		return 0, fmt.Errorf("failed to encode request: %w", err)
	}

	if _, err := stream.Write(reqBytes); err != nil {
		return 0, fmt.Errorf("failed to write request: %w", err)
	}

	// Read the response
	headerPrefix := make([]byte, 4)
	if _, err := io.ReadFull(stream, headerPrefix); err != nil {
		return 0, fmt.Errorf("failed to read header length prefix: %w", err)
	}
	headerTotalLength := binary.LittleEndian.Uint32(headerPrefix)
	if headerTotalLength < 4 {
		return 0, fmt.Errorf("invalid header length %d", headerTotalLength)
	}

	// Allocate header buffer.
	headerBuf := make([]byte, headerTotalLength)

	// Copy prefix into headerBuf.
	copy(headerBuf, headerPrefix)
	// Read the remainder of the header.
	if _, err := io.ReadFull(stream, headerBuf[4:]); err != nil {
		return 0, fmt.Errorf("failed to read full header: %w", err)
	}

	// Decode the header.
	var resp Response
	if err := resp.Decode(headerBuf); err != nil {
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}

	// Handle error responses
	if resp.Status != 213 {
		var serErr SerializableError
		if err := serErr.Decode(resp.Data); err == nil {
			return 0, UnwrapError(serErr)
		}
		return 0, fmt.Errorf("RPC error: status %d", resp.Status)
	}

	// Read the length prefix
	var length uint32
	if err := binary.Read(stream, binary.LittleEndian, &length); err != nil {
		return 0, fmt.Errorf("failed to read length prefix: %w", err)
	}

	// Ensure we don't exceed buffer capacity
	bytesToRead := min(int(length), len(buffer))

	// Read the data
	totalRead := 0
	for totalRead < bytesToRead {
		n, err := stream.Read(buffer[totalRead:bytesToRead])
		if n > 0 {
			totalRead += n
		}
		if err != nil {
			if err == io.EOF && totalRead == bytesToRead {
				return totalRead, nil
			}
			return totalRead, fmt.Errorf("read error after %d/%d bytes: %w",
				totalRead, bytesToRead, err)
		}
	}

	return totalRead, nil
}
