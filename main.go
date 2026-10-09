package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"bcurl/proto"
)

// verbose controls -v output to stderr.
var verbose bool

// xxdDump prints a hexdump in xxd style (16 bytes per row with ASCII) to stderr.
// If limitBytes >= 0, dump only the first limitBytes bytes of data.
func xxdDump(data []byte, limitBytes int) {
	n := len(data)
	if limitBytes >= 0 && limitBytes < n {
		n = limitBytes
	}
	for off := 0; off < n; off += 16 {
		end := off + 16
		if end > n {
			end = n
		}
		chunk := data[off:end]
		fmt.Fprintf(os.Stderr, "%08x: ", off)
		for i, b := range chunk {
			if i == 8 {
				fmt.Fprintf(os.Stderr, " ")
			}
			fmt.Fprintf(os.Stderr, "%02x ", b)
		}
		// Pad short rows.
		for i := len(chunk); i < 16; i++ {
			if i == 8 {
				fmt.Fprintf(os.Stderr, " ")
			}
			fmt.Fprintf(os.Stderr, "   ")
		}
		fmt.Fprintf(os.Stderr, " |")
		for _, b := range chunk {
			if b >= 0x20 && b < 0x7f {
				fmt.Fprintf(os.Stderr, "%c", b)
			} else {
				fmt.Fprintf(os.Stderr, ".")
			}
		}
		fmt.Fprintf(os.Stderr, "|\n")
	}
}

// rawHeader reconstructs the 12-byte frame header from a FrameHeader struct.
func rawHeader(fh proto.FrameHeader) []byte {
	raw := make([]byte, 12)
	raw[0] = fh.Version
	raw[1] = fh.Type
	raw[2] = fh.Flags
	raw[3] = fh.Reserved
	binary.BigEndian.PutUint32(raw[4:8], fh.RequestID)
	binary.BigEndian.PutUint32(raw[8:12], fh.Length)
	return raw
}

// frameTypeName returns a human-readable frame type name.
func frameTypeName(t uint8) string {
	switch t {
	case proto.TypeREQUEST:
		return "REQUEST"
	case proto.TypeRESPONSE:
		return "RESPONSE"
	case proto.TypeDATA:
		return "DATA"
	default:
		return fmt.Sprintf("UNKNOWN(0x%02x)", t)
	}
}

// logSent logs a sent frame to stderr in verbose mode.
func logSent(fh proto.FrameHeader, payload []byte) {
	typeName := frameTypeName(fh.Type)
	fmt.Fprintf(os.Stderr, "> %s ver=%d flags=0x%02x id=%d len=%d\n",
		typeName, fh.Version, fh.Flags, fh.RequestID, fh.Length)

	// Payload summary.
	switch fh.Type {
	case proto.TypeREQUEST:
		if len(payload) >= 3 {
			methodStr := "UNKNOWN"
			if payload[0] == 0x01 {
				methodStr = "GET"
			}
			pathLen := int(binary.BigEndian.Uint16(payload[1:3]))
			path := ""
			if 3+pathLen <= len(payload) {
				path = string(payload[3 : 3+pathLen])
			}
			fmt.Fprintf(os.Stderr, "  method=%s path=%s\n", methodStr, path)
			if 3+pathLen < len(payload) {
				hdrs, err := proto.DecodeHeaderBlock(payload[3+pathLen:])
				if err == nil {
					for _, h := range hdrs {
						fmt.Fprintf(os.Stderr, "  %s: %s\n", h.Name, h.Value)
					}
				}
			}
		}
	case proto.TypeDATA:
		fmt.Fprintf(os.Stderr, "  DATA %d bytes\n", len(payload))
	}

	all := append(rawHeader(fh), payload...)
	xxdDump(all, -1)
}

// logReceived logs a received frame to stderr in verbose mode.
// rawHdr is the 12 raw bytes already read off the wire.
func logReceived(fh proto.FrameHeader, rawHdr []byte, payload []byte) {
	if fh.Type != proto.TypeREQUEST && fh.Type != proto.TypeRESPONSE && fh.Type != proto.TypeDATA {
		fmt.Fprintf(os.Stderr, "< UNKNOWN type=0x%02x len=%d (skipped)\n", fh.Type, fh.Length)
		all := append(rawHdr, payload...)
		xxdDump(all, -1)
		return
	}
	typeName := frameTypeName(fh.Type)
	fmt.Fprintf(os.Stderr, "< %s ver=%d flags=0x%02x id=%d len=%d\n",
		typeName, fh.Version, fh.Flags, fh.RequestID, fh.Length)

	switch fh.Type {
	case proto.TypeRESPONSE:
		status, hdrs, err := proto.DecodeRESPONSE(payload)
		if err == nil {
			fmt.Fprintf(os.Stderr, "  status=%d\n", status)
			for _, h := range hdrs {
				fmt.Fprintf(os.Stderr, "  %s: %s\n", h.Name, h.Value)
			}
		}
	case proto.TypeDATA:
		fmt.Fprintf(os.Stderr, "  DATA %d bytes\n", len(payload))
	}

	all := append(rawHdr, payload...)
	if fh.Type == proto.TypeDATA && len(payload) > 64 {
		more := len(payload) - 64
		xxdDump(all, 12+64)
		fmt.Fprintf(os.Stderr, "  ... (%d more bytes)\n", more)
	} else {
		xxdDump(all, -1)
	}
}

// sendFrame writes a complete frame and logs it if verbose.
func sendFrame(w io.Writer, fh proto.FrameHeader, payload []byte) error {
	if _, err := proto.WriteFrameHeader(w, fh); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	if verbose {
		logSent(fh, payload)
	}
	return nil
}

// sendUnknownFrame sends a type=0x7F frame with 8 bytes of junk payload.
func sendUnknownFrame(w io.Writer, requestID uint32) error {
	payload := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}
	fh := proto.FrameHeader{
		Version:   proto.Version,
		Type:      0x7F,
		Flags:     0,
		Reserved:  0,
		RequestID: requestID,
		Length:    uint32(len(payload)),
	}
	return sendFrame(w, fh, payload)
}

// sendREQUEST sends a REQUEST frame for the given path.
// Headers sent (in order): host, user-agent, accept.
func sendREQUEST(w io.Writer, requestID uint32, host, path string) error {
	headers := []proto.Header{
		{Name: "host", Value: host},
		{Name: "user-agent", Value: "bcurl/1.0"},
		{Name: "accept", Value: "*/*"},
	}
	payload := proto.EncodeREQUEST(path, headers)
	fh := proto.FrameHeader{
		Version:   proto.Version,
		Type:      proto.TypeREQUEST,
		Flags:     0,
		Reserved:  0,
		RequestID: requestID,
		Length:    uint32(len(payload)),
	}
	return sendFrame(w, fh, payload)
}

// readFrame reads one complete frame (header + payload) from r.
// Validates version and the 1 MiB length cap.
// Returns (FrameHeader, raw12bytes, payload, error).
func readFrame(r io.Reader) (proto.FrameHeader, []byte, []byte, error) {
	fh, rawHdr, err := proto.ReadFrameHeader(r)
	if err != nil {
		return fh, rawHdr, nil, err
	}
	if fh.Version != proto.Version {
		return fh, rawHdr, nil, fmt.Errorf("protocol error: unexpected version %d (want %d)", fh.Version, proto.Version)
	}
	if fh.Length > proto.MaxPayload {
		return fh, rawHdr, nil, fmt.Errorf("protocol error: payload length %d exceeds 1 MiB cap", fh.Length)
	}
	var payload []byte
	if fh.Length > 0 {
		payload = make([]byte, fh.Length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return fh, rawHdr, nil, err
		}
	}
	return fh, rawHdr, payload, nil
}

// doRequest performs one full request/response exchange over an already-open conn.
// Body bytes are written to stdout as they arrive.
// Returns the HTTP status code, or an error for network/protocol failures.
func doRequest(conn net.Conn, requestID uint32, host, path string, injectUnknown bool) (uint16, error) {
	if injectUnknown {
		if err := sendUnknownFrame(conn, requestID); err != nil {
			return 0, fmt.Errorf("send unknown frame: %w", err)
		}
	}

	if err := sendREQUEST(conn, requestID, host, path); err != nil {
		return 0, fmt.Errorf("send request: %w", err)
	}

	var status uint16
	gotResponse := false

	for {
		fh, rawHdr, payload, err := readFrame(conn)
		if err != nil {
			return 0, fmt.Errorf("read frame: %w", err)
		}

		// Log received frame before dispatch.
		if verbose {
			logReceived(fh, rawHdr, payload)
		}

		switch fh.Type {
		case proto.TypeRESPONSE:
			if fh.RequestID != requestID {
				return 0, fmt.Errorf("protocol error: request_id mismatch (want %d, got %d)", requestID, fh.RequestID)
			}
			var hdrs []proto.Header
			status, hdrs, err = proto.DecodeRESPONSE(payload)
			if err != nil {
				return 0, fmt.Errorf("decode response: %w", err)
			}
			_ = hdrs
			gotResponse = true
			if fh.HasEndStream() {
				return status, nil
			}

		case proto.TypeDATA:
			if !gotResponse {
				return 0, fmt.Errorf("protocol error: DATA frame before RESPONSE")
			}
			if fh.RequestID != requestID {
				return 0, fmt.Errorf("protocol error: request_id mismatch (want %d, got %d)", requestID, fh.RequestID)
			}
			if len(payload) > 0 {
				if _, err := os.Stdout.Write(payload); err != nil {
					return 0, fmt.Errorf("write stdout: %w", err)
				}
			}
			if fh.HasEndStream() {
				return status, nil
			}

		default:
			// Unknown frame type: payload already read; skip per spec section 5.
			continue
		}
	}
}

func main() {
	flag.BoolVar(&verbose, "v", false, "verbose: print annotated hexdumps to stderr")
	injectUnknown := flag.Bool("inject-unknown", false, "inject a 0x7F unknown frame before each REQUEST")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: bcurl [-v] [--inject-unknown] <host:port/path> [/path2 ...]\n")
		os.Exit(3)
	}

	// Parse first argument: host:port/path
	first := args[0]
	slashIdx := strings.Index(first, "/")
	if slashIdx < 0 {
		fmt.Fprintf(os.Stderr, "error: first argument must be in the form host:port/path\n")
		os.Exit(3)
	}
	hostPort := first[:slashIdx]
	firstPath := first[slashIdx:]

	paths := make([]string, 0, len(args))
	paths = append(paths, firstPath)
	for _, a := range args[1:] {
		paths = append(paths, a)
	}

	// Open exactly ONE TCP connection per invocation.
	conn, err := net.Dial("tcp", hostPort)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect %s: %v\n", hostPort, err)
		os.Exit(3)
	}
	defer conn.Close()

	worstCode := 0
	requestID := uint32(1)

	for _, path := range paths {
		status, reqErr := doRequest(conn, requestID, hostPort, path, *injectUnknown)
		requestID++

		if reqErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", reqErr)
			os.Exit(3)
		}

		var code int
		switch {
		case status >= 200 && status < 400:
			code = 0
		case status >= 400 && status < 500:
			code = 1
		case status >= 500:
			code = 2
		default:
			code = 3
		}
		if code > worstCode {
			worstCode = code
		}
	}

	os.Exit(worstCode)
}