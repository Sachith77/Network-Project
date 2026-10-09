package proto

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ReadFrameHeader reads and parses the 12-byte fixed frame header.
// It returns the parsed header and the raw 12 bytes (for verbose logging).
func ReadFrameHeader(r io.Reader) (FrameHeader, []byte, error) {
	raw := make([]byte, 12)
	if _, err := io.ReadFull(r, raw); err != nil {
		return FrameHeader{}, nil, err
	}
	fh := FrameHeader{
		Version:   raw[0],
		Type:      raw[1],
		Flags:     raw[2],
		Reserved:  raw[3],
		RequestID: binary.BigEndian.Uint32(raw[4:8]),
		Length:    binary.BigEndian.Uint32(raw[8:12]),
	}
	return fh, raw, nil
}

// WriteFrameHeader encodes and writes the 12-byte fixed frame header.
// Returns the raw bytes written so callers can use them for verbose logging.
func WriteFrameHeader(w io.Writer, fh FrameHeader) ([]byte, error) {
	raw := make([]byte, 12)
	raw[0] = fh.Version
	raw[1] = fh.Type
	raw[2] = fh.Flags
	raw[3] = fh.Reserved
	binary.BigEndian.PutUint32(raw[4:8], fh.RequestID)
	binary.BigEndian.PutUint32(raw[8:12], fh.Length)
	_, err := w.Write(raw)
	return raw, err
}

// EncodeHeaderBlock serialises a slice of headers per spec section 4.
// Senders MUST use the indexed form for names in the static table.
func EncodeHeaderBlock(headers []Header) []byte {
	var buf []byte
	buf = append(buf, uint8(len(headers)))
	for _, h := range headers {
		idx := StaticIndex(h.Name)
		if idx != 0 {
			// Indexed name: 0x80 | index, then u16 value_len, value bytes.
			buf = append(buf, 0x80|idx)
		} else {
			// Literal name: 0x00, u8 name_len, name bytes.
			buf = append(buf, 0x00)
			buf = append(buf, uint8(len(h.Name)))
			buf = append(buf, []byte(h.Name)...)
		}
		vb := []byte(h.Value)
		vlen := make([]byte, 2)
		binary.BigEndian.PutUint16(vlen, uint16(len(vb)))
		buf = append(buf, vlen...)
		buf = append(buf, vb...)
	}
	return buf
}

// DecodeHeaderBlock parses a header block from a byte slice per spec section 4.
func DecodeHeaderBlock(data []byte) ([]Header, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("header block too short")
	}
	count := int(data[0])
	off := 1
	headers := make([]Header, 0, count)
	for i := 0; i < count; i++ {
		if off >= len(data) {
			return nil, fmt.Errorf("header block truncated at entry %d", i)
		}
		fb := data[off]
		off++
		var name string
		if fb&0x80 != 0 {
			// Indexed name.
			idx := fb & 0x7F
			if idx < 1 || idx > 10 {
				return nil, fmt.Errorf("invalid static table index %d", idx)
			}
			name = StaticTable[idx]
		} else if fb == 0x00 {
			// Literal name.
			if off >= len(data) {
				return nil, fmt.Errorf("truncated literal name length")
			}
			nameLen := int(data[off])
			off++
			if off+nameLen > len(data) {
				return nil, fmt.Errorf("truncated literal name bytes")
			}
			name = string(data[off : off+nameLen])
			off += nameLen
		} else {
			return nil, fmt.Errorf("malformed header entry first byte 0x%02x", fb)
		}
		// Read u16 value_len then value bytes.
		if off+2 > len(data) {
			return nil, fmt.Errorf("truncated value length")
		}
		vlen := int(binary.BigEndian.Uint16(data[off : off+2]))
		off += 2
		if off+vlen > len(data) {
			return nil, fmt.Errorf("truncated value bytes")
		}
		val := string(data[off : off+vlen])
		off += vlen
		headers = append(headers, Header{Name: name, Value: val})
	}
	return headers, nil
}

// EncodeREQUEST builds the full payload for a REQUEST frame.
// method is always 0x01 (GET). path must start with '/'.
func EncodeREQUEST(path string, headers []Header) []byte {
	var buf []byte
	buf = append(buf, 0x01) // method = GET
	pb := []byte(path)
	pathLen := make([]byte, 2)
	binary.BigEndian.PutUint16(pathLen, uint16(len(pb)))
	buf = append(buf, pathLen...)
	buf = append(buf, pb...)
	buf = append(buf, EncodeHeaderBlock(headers)...)
	return buf
}

// DecodeRESPONSE parses a RESPONSE payload: status u16, then header block.
func DecodeRESPONSE(data []byte) (status uint16, headers []Header, err error) {
	if len(data) < 2 {
		return 0, nil, fmt.Errorf("response payload too short")
	}
	status = binary.BigEndian.Uint16(data[0:2])
	headers, err = DecodeHeaderBlock(data[2:])
	return status, headers, err
}