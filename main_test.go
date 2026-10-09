package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"sync"
	"testing"

	"bcurl/proto"
)

// ---- helpers ----

// buildResponseFrame builds a raw RESPONSE frame.
func buildResponseFrame(requestID uint32, status uint16, hdrs []proto.Header, flags uint8) []byte {
	hdrBlock := proto.EncodeHeaderBlock(hdrs)
	payload := make([]byte, 2+len(hdrBlock))
	binary.BigEndian.PutUint16(payload[0:2], status)
	copy(payload[2:], hdrBlock)
	raw := make([]byte, 12+len(payload))
	raw[0] = proto.Version
	raw[1] = proto.TypeRESPONSE
	raw[2] = flags
	raw[3] = 0
	binary.BigEndian.PutUint32(raw[4:8], requestID)
	binary.BigEndian.PutUint32(raw[8:12], uint32(len(payload)))
	copy(raw[12:], payload)
	return raw
}

// buildDataFrame builds a raw DATA frame.
func buildDataFrame(requestID uint32, data []byte, flags uint8) []byte {
	raw := make([]byte, 12+len(data))
	raw[0] = proto.Version
	raw[1] = proto.TypeDATA
	raw[2] = flags
	raw[3] = 0
	binary.BigEndian.PutUint32(raw[4:8], requestID)
	binary.BigEndian.PutUint32(raw[8:12], uint32(len(data)))
	copy(raw[12:], data)
	return raw
}

// buildUnknownFrame builds a raw unknown (type=0x7F) frame with 8 zero bytes.
func buildUnknownFrame(requestID uint32) []byte {
	payload := make([]byte, 8)
	raw := make([]byte, 20)
	raw[0] = proto.Version
	raw[1] = 0x7F
	raw[2] = 0
	raw[3] = 0
	binary.BigEndian.PutUint32(raw[4:8], requestID)
	binary.BigEndian.PutUint32(raw[8:12], 8)
	copy(raw[12:], payload)
	return raw
}

// ---- Test 1: Encoding GET /index.html matches SPEC section 8 example ----
// Only host and user-agent headers (spec example has 2 headers).

func TestEncodeRequest_SpecExample(t *testing.T) {
	headers := []proto.Header{
		{Name: "host", Value: "localhost:9000"},
		{Name: "user-agent", Value: "bcurl/1.0"},
	}
	payload := proto.EncodeREQUEST("/index.html", headers)

	// Expected payload bytes (44 total) derived from SPEC section 8:
	// 01                                     method=GET
	// 00 0b                                  path_len=11
	// 2f 69 6e 64 65 78 2e 68 74 6d 6c       "/index.html"
	// 02                                     header count=2
	// 83 00 0e 6c6f63616c686f73743a39303030  [3]host="localhost:9000"
	// 84 00 09 62637572 6c2f312e 30          [4]user-agent="bcurl/1.0"
	expected := []byte{
		0x01,
		0x00, 0x0b,
		0x2f, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x2e, 0x68, 0x74, 0x6d, 0x6c,
		0x02,
		0x83,
		0x00, 0x0e,
		0x6c, 0x6f, 0x63, 0x61, 0x6c, 0x68, 0x6f, 0x73, 0x74, 0x3a, 0x39, 0x30, 0x30, 0x30,
		0x84,
		0x00, 0x09,
		0x62, 0x63, 0x75, 0x72, 0x6c, 0x2f, 0x31, 0x2e, 0x30,
	}
	if !bytes.Equal(payload, expected) {
		t.Errorf("payload mismatch\ngot:  %x\nwant: %x", payload, expected)
	}

	// Verify full frame = 12 + 44 = 56 bytes with correct header.
	var buf bytes.Buffer
	fh := proto.FrameHeader{
		Version: proto.Version, Type: proto.TypeREQUEST,
		Flags: 0, Reserved: 0, RequestID: 1, Length: uint32(len(payload)),
	}
	proto.WriteFrameHeader(&buf, fh)
	buf.Write(payload)
	full := buf.Bytes()

	expectedHeader := []byte{
		0x01, 0x01, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x2c,
	}
	expectedFull := append(expectedHeader, expected...)
	if !bytes.Equal(full, expectedFull) {
		t.Errorf("full frame mismatch\ngot:  %x\nwant: %x", full, expectedFull)
	}
}

// ---- Test 2: Decode exact SPEC section 8 404 response bytes ----

func TestDecodeResponse_SpecExample(t *testing.T) {
	// 404 response bytes from SPEC section 8 (12 header + 7 payload = 19 bytes).
	raw := []byte{
		0x01, 0x02, 0x01, 0x00,
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x07,
		// payload: status=404 (0x0194), 1 header, [2]content-length="0"
		0x01, 0x94,
		0x01,
		0x82,
		0x00, 0x01,
		0x30,
	}
	r := bytes.NewReader(raw)
	fh, _, err := proto.ReadFrameHeader(r)
	if err != nil {
		t.Fatalf("ReadFrameHeader: %v", err)
	}
	if fh.Version != 1 {
		t.Errorf("version: got %d, want 1", fh.Version)
	}
	if fh.Type != proto.TypeRESPONSE {
		t.Errorf("type: got %d, want %d (RESPONSE)", fh.Type, proto.TypeRESPONSE)
	}
	if !fh.HasEndStream() {
		t.Error("END_STREAM flag should be set")
	}
	if fh.RequestID != 1 {
		t.Errorf("request_id: got %d, want 1", fh.RequestID)
	}
	if fh.Length != 7 {
		t.Errorf("length: got %d, want 7", fh.Length)
	}
	payload := make([]byte, fh.Length)
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	status, hdrs, err := proto.DecodeRESPONSE(payload)
	if err != nil {
		t.Fatalf("DecodeRESPONSE: %v", err)
	}
	if status != 404 {
		t.Errorf("status: got %d, want 404", status)
	}
	if len(hdrs) != 1 {
		t.Fatalf("header count: got %d, want 1", len(hdrs))
	}
	if hdrs[0].Name != "content-length" {
		t.Errorf("header[0].Name: got %q, want %q", hdrs[0].Name, "content-length")
	}
	if hdrs[0].Value != "0" {
		t.Errorf("header[0].Value: got %q, want %q", hdrs[0].Value, "0")
	}
}

// ---- Test 3: Unknown frame + RESPONSE 200 + two DATA frames ----

func TestClient_UnknownThenDataThenEndStream(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Drain the incoming REQUEST first.
		hdr := make([]byte, 12)
		io.ReadFull(conn, hdr)
		fh, _, _ := proto.ReadFrameHeader(bytes.NewReader(hdr))
		if fh.Length > 0 {
			io.ReadFull(conn, make([]byte, fh.Length))
		}
		// Send: unknown 0x7F frame, RESPONSE 200 (no END_STREAM), DATA1, DATA2+END_STREAM.
		conn.Write(buildUnknownFrame(1))
		conn.Write(buildResponseFrame(1, 200, []proto.Header{{Name: "content-length", Value: "11"}}, 0))
		conn.Write(buildDataFrame(1, []byte("hello "), 0))
		conn.Write(buildDataFrame(1, []byte("world"), proto.FlagENDStream))
	}()

	origStdout := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		pw.Close()
		os.Stdout = origStdout
		t.Fatal(err)
	}

	status, reqErr := doRequest(conn, 1, ln.Addr().String(), "/index.html", false)
	conn.Close()
	pw.Close()

	var outBuf bytes.Buffer
	io.Copy(&outBuf, pr)
	os.Stdout = origStdout

	if reqErr != nil {
		t.Fatalf("doRequest error: %v", reqErr)
	}
	if status != 200 {
		t.Errorf("status: got %d, want 200", status)
	}
	if outBuf.String() != "hello world" {
		t.Errorf("body: got %q, want %q", outBuf.String(), "hello world")
	}
}

// ---- Test 4: 404 response produces exit code 1 ----

func TestClient_404ExitCode(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		hdr := make([]byte, 12)
		io.ReadFull(conn, hdr)
		fh, _, _ := proto.ReadFrameHeader(bytes.NewReader(hdr))
		if fh.Length > 0 {
			io.ReadFull(conn, make([]byte, fh.Length))
		}
		conn.Write(buildResponseFrame(1, 404, []proto.Header{{Name: "content-length", Value: "0"}}, proto.FlagENDStream))
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	status, reqErr := doRequest(conn, 1, ln.Addr().String(), "/missing.html", false)
	if reqErr != nil {
		t.Fatalf("unexpected error: %v", reqErr)
	}
	if status != 404 {
		t.Errorf("status: got %d, want 404", status)
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
	if code != 1 {
		t.Errorf("exit code for 404: got %d, want 1", code)
	}
}

// ---- Test 5: Two paths => one connection, request_ids 1 and 2 ----

func TestClient_TwoPaths_OneConnection(t *testing.T) {
	var mu sync.Mutex
	connCount := 0
	var receivedIDs []uint32

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		mu.Lock()
		connCount++
		mu.Unlock()
		defer conn.Close()

		for i := 0; i < 2; i++ {
			hdr := make([]byte, 12)
			if _, err := io.ReadFull(conn, hdr); err != nil {
				return
			}
			fh, _, _ := proto.ReadFrameHeader(bytes.NewReader(hdr))
			mu.Lock()
			receivedIDs = append(receivedIDs, fh.RequestID)
			mu.Unlock()
			if fh.Length > 0 {
				io.ReadFull(conn, make([]byte, fh.Length))
			}
			conn.Write(buildResponseFrame(fh.RequestID, 404,
				[]proto.Header{{Name: "content-length", Value: "0"}},
				proto.FlagENDStream))
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	doRequest(conn, 1, ln.Addr().String(), "/a.html", false)
	doRequest(conn, 2, ln.Addr().String(), "/b.html", false)

	mu.Lock()
	defer mu.Unlock()

	if connCount != 1 {
		t.Errorf("connection count: got %d, want 1", connCount)
	}
	if len(receivedIDs) != 2 {
		t.Fatalf("request count: got %d, want 2", len(receivedIDs))
	}
	if receivedIDs[0] != 1 {
		t.Errorf("first request_id: got %d, want 1", receivedIDs[0])
	}
	if receivedIDs[1] != 2 {
		t.Errorf("second request_id: got %d, want 2", receivedIDs[1])
	}
}

// ---- Test 6: version=2 frame causes protocol error (exit code 3) ----

func TestClient_Version2_ProtocolError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		hdr := make([]byte, 12)
		io.ReadFull(conn, hdr)
		fh, _, _ := proto.ReadFrameHeader(bytes.NewReader(hdr))
		if fh.Length > 0 {
			io.ReadFull(conn, make([]byte, fh.Length))
		}
		// Reply with version=2 (framing error).
		bad := []byte{
			0x02, 0x02, 0x01, 0x00,
			0x00, 0x00, 0x00, 0x01,
			0x00, 0x00, 0x00, 0x03,
			0x01, 0x94,
			0x00,
		}
		conn.Write(bad)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, reqErr := doRequest(conn, 1, ln.Addr().String(), "/index.html", false)
	if reqErr == nil {
		t.Fatal("expected protocol error for version=2, got nil")
	}
}