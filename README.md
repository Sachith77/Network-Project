# bcurl

A command-line **client** for **BHTTP/1** — Binary HTTP over TCP.
The protocol is defined in [SPEC.md](SPEC.md).

This is one half of a two-person course project.
The server half (by teammate Sanjay Sagar Reddy) lives at:
**https://github.com/SanjaySagarReddy/NetworkArchitecture_Project**

Both sides implement the same protocol spec independently and are interoperable.

## Build

```
go build -o bcurl .
```

On Windows:

```
go build -o bcurl.exe .
```

## Run

```
bcurl [-v] [--inject-unknown] <host:port/path> [/path2 /path3 ...]
```

### Examples

Fetch a single file:

```
bcurl localhost:9000/index.html
```

Fetch multiple paths over the **same** TCP connection (demonstrates keep-alive):

```
bcurl localhost:9000/index.html /about.html /style.css
```

Verbose mode (annotated hexdumps of every frame to stderr, body to stdout):

```
bcurl -v localhost:9000/index.html
```

Inject an unknown frame (type=0x7F) before each REQUEST to verify the server skips it:

```
bcurl --inject-unknown localhost:9000/index.html
```

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | All responses 2xx/3xx |
| 1 | At least one 4xx response |
| 2 | At least one 5xx response |
| 3 | Network or protocol error |

With multiple paths, bcurl keeps going after a 4xx/5xx and exits with the worst code seen.

## Test

```
go test ./...
```

## Code layout

```
.
+-- SPEC.md          # Protocol specification (do not edit)
+-- main.go          # CLI entry-point and request/response loop
+-- main_test.go     # Hand-written byte-level tests (no server implementation)
+-- proto/
    +-- types.go     # Constants, static table, Header, FrameHeader types
    +-- frame.go     # Frame read/write, header block encode/decode, REQUEST/RESPONSE helpers
+-- go.mod
```

## Protocol

See [SPEC.md](SPEC.md) for the full BHTTP/1 specification.