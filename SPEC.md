BHTTP/1 — Binary HTTP over TCP
1. Overview
One TCP connection carries any number of request/response exchanges, strictly sequential (client sends a request, reads the full response, then may send the next). All integers are big-endian (network byte order). Strings are UTF-8, never NUL-terminated.
2. Frame header (12 bytes, fixed)
Offset Size Field Notes 0 1 version MUST be 0x01 1 1 type see §3 2 1 flags bit 0x01 = END_STREAM; other bits: send 0, ignore on receive 3 1 reserved send 0x00, ignore on receive 4 4 request_id u32, chosen by client, starts at 1, +1 per request; 0 = connection-level 8 4 length u32, payload length in bytes (header not included); MAX 1,048,576
Why these widths (vs HTTP/2's 24/8/8/31):
12 bytes keeps every field byte-aligned and the header 4-byte aligned; a hexdump is readable by eye. HTTP/2 packs into 9 bytes because it is optimised for the wire, we optimise for being implementable and debuggable in an evening.
Explicit version byte: lets a receiver reject a future incompatible framing immediately.
8-bit type: 256 frame types is plenty; unknown types are skipped (§5), which is our v2 path.
8-bit reserved byte: free space for v2 without changing the header size.
32-bit request_id: we do not multiplex, but tagging each frame lets a receiver detect a desync and leaves room for multiplexing in v2. No reserved high bit is needed (unlike HTTP/2).
32-bit length with a 1 MiB cap: u32 avoids 24-bit arithmetic; the cap bounds receiver memory. Large bodies are split across DATA frames.
3. Frame types
0x01 REQUEST (client -> server). flags = 0. method u8 0x01 = GET (only method in v1) path_len u16 path bytes must start with "/" headers header block (ss4)
0x02 RESPONSE (server -> client). status u16 200, 400, 404, 500 headers header block (ss4) If END_STREAM is set, there is no body and the exchange is complete. Otherwise one or more DATA frames follow.
0x03 DATA (server -> client). Payload = raw body bytes (may be empty). END_STREAM set on the last DATA frame of the response. Senders SHOULD use payloads of at most 16,384 bytes.
All other values: unknown, so they MUST be skipped (ss5).
4. Header block
count u8 number of entries (0-255) entries repeated count times, each one of:
Indexed name: 1 byte 0x80 | index (index 1-10, from static table) u16 value_len, value bytes Literal name: 1 byte 0x00 u8 name_len, name bytes (lowercase ASCII) u16 value_len, value bytes
Any other first byte is malformed.
Static table (the ten names we actually send): 1 content-type 2 content-length 3 host 4 user-agent 5 accept 6 server 7 date 8 last-modified 9 connection 10 cache-control
Senders MUST use the indexed form for names in the table. Receivers MUST accept both forms.
5. Unknown frames (MUST)
A receiver that meets a frame type it does not know MUST read and discard exactly length payload bytes and continue reading the next frame. It MUST NOT close the connection or report an error. This is how v2 adds frame types without breaking v1.
6. Server behaviour
Map path to a file under the document root. "/" means "/index.html".
A path that is not found, or is a directory, gets 404.
A path that escapes the root (e.g. contains ".." after cleaning) gets 400.
A 200 response carries content-type and content-length, followed by DATA frames.
Error responses (400/404/500) are RESPONSE with END_STREAM and content-length "0".
Malformed REQUEST payload (bad method, truncated fields, bad header entry): reply 400 with the same request_id and keep the connection open.
Framing errors (version != 1, length > 1 MiB): the stream cannot be resynchronised. Send RESPONSE 400 with request_id 0 and END_STREAM, then close.
Keep the connection open after each response until the client closes it.
7. Client behaviour
Send one REQUEST; read frames until a RESPONSE with END_STREAM, or DATA with END_STREAM.
Frames whose request_id does not match the current request are a protocol error.
Never open more than one connection per invocation.
8. Worked example: GET /index.html, then 404
Request (client -> server), 12 + 44 bytes: 01 01 00 00 00 00 00 01 00 00 00 2c ver=1 type=REQUEST flags=0 rsv=0 id=1 len=44 01 method=GET 00 0b 2f 69 6e 64 65 78 2e 68 74 6d 6c path_len=11 "/index.html" 02 2 headers 83 00 0e 6c 6f 63 61 6c 68 6f 73 74 3a 39 30 30 30 [3]host = "localhost:9000" 84 00 09 62 63 75 72 6c 2f 31 2e 30 [4]user-agent = "bcurl/1.0"
404 response (server -> client), 12 + 7 bytes: 01 02 01 00 00 00 00 01 00 00 00 07 ver=1 type=RESPONSE flags=END_STREAM id=1 len=7 01 94 status=404 01 82 00 01 30 1 header: [2]content-length = "0"
