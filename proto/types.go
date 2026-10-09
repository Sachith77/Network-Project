package proto

// Frame type constants.
const (
	TypeREQUEST  = 0x01
	TypeRESPONSE = 0x02
	TypeDATA     = 0x03
)

// Flag bits.
const FlagENDStream = 0x01

// Version byte.
const Version = 0x01

// MaxPayload is the maximum allowed payload length (1 MiB = 1,048,576 bytes).
const MaxPayload = 1 << 20

// StaticTable holds indexed header names (1-based, per spec section 4).
// Index 0 is unused.
var StaticTable = [11]string{
	"",               // 0 - unused
	"content-type",   // 1
	"content-length", // 2
	"host",           // 3
	"user-agent",     // 4
	"accept",         // 5
	"server",         // 6
	"date",           // 7
	"last-modified",  // 8
	"connection",     // 9
	"cache-control",  // 10
}

// StaticIndex returns the 1-based index of name in the static table, or 0.
func StaticIndex(name string) uint8 {
	for i := 1; i <= 10; i++ {
		if StaticTable[i] == name {
			return uint8(i)
		}
	}
	return 0
}

// Header is a single name/value pair.
type Header struct {
	Name  string
	Value string
}

// FrameHeader is the 12-byte fixed frame header.
type FrameHeader struct {
	Version   uint8
	Type      uint8
	Flags     uint8
	Reserved  uint8
	RequestID uint32
	Length    uint32
}

// HasEndStream reports whether the END_STREAM flag (0x01) is set.
func (h FrameHeader) HasEndStream() bool {
	return h.Flags&FlagENDStream != 0
}
