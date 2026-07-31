package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// frameFormat identifies the stdio framing used by the MCP host.
type frameFormat int

const (
	frameNewline frameFormat = iota
	frameContentLength
)

// CompatibleStdioTransport is a stdio transport that accepts both the MCP
// standard newline-delimited JSON framing and the Content-Length framing used
// by some hosts (for example the Claude Code desktop sidecar). Responses are
// written using the framing detected from the host's first request, so both
// newline-only hosts and Content-Length-only hosts can connect.
type CompatibleStdioTransport struct{}

// Connect implements mcp.Transport.
func (*CompatibleStdioTransport) Connect(context.Context) (mcp.Connection, error) {
	return newCompatConn(os.Stdin, os.Stdout), nil
}

type compatMsg struct {
	msg jsonrpc.Message
	err error
}

type compatConn struct {
	reader *bufio.Reader
	writer io.Writer

	formatMu       sync.Mutex
	format         frameFormat
	formatDetected bool

	writeMu sync.Mutex

	decoderMu sync.Mutex
	decoder   *jsonDecoder

	closeOnce sync.Once
	closed    chan struct{}

	incoming chan compatMsg
}

func newCompatConn(reader io.Reader, writer io.Writer) *compatConn {
	c := &compatConn{
		reader:   bufio.NewReader(reader),
		writer:   writer,
		format:   frameNewline,
		closed:   make(chan struct{}),
		incoming: make(chan compatMsg),
	}
	go c.readLoop()
	return c
}

func (c *compatConn) SessionID() string { return "" }

func (c *compatConn) Read(_ context.Context) (jsonrpc.Message, error) {
	select {
	case result := <-c.incoming:
		return result.msg, result.err
	case <-c.closed:
		return nil, mcp.ErrConnectionClosed
	}
}

func (c *compatConn) Write(_ context.Context, msg jsonrpc.Message) error {
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}
	c.formatMu.Lock()
	format := c.format
	c.formatMu.Unlock()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	switch format {
	case frameContentLength:
		if _, err := fmt.Fprintf(c.writer, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
			return err
		}
		_, err := c.writer.Write(data)
		return err
	default:
		if _, err := c.writer.Write(data); err != nil {
			return err
		}
		_, err := c.writer.Write([]byte{'\n'})
		return err
	}
}

func (c *compatConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
	})
	return nil
}

func (c *compatConn) readLoop() {
	for {
		msg, err := c.readOne()
		select {
		case c.incoming <- compatMsg{msg: msg, err: err}:
		case <-c.closed:
			return
		}
		if err != nil {
			return
		}
	}
}

func (c *compatConn) readOne() (jsonrpc.Message, error) {
	c.formatMu.Lock()
	detected := c.formatDetected
	format := c.format
	c.formatMu.Unlock()
	if detected {
		if format == frameContentLength {
			return c.readContentLength()
		}
		return c.readNewline()
	}
	// Only the first message probes the framing. Once a framing is chosen it is
	// fixed for the life of the connection: re-probing on every message would
	// race with the newline decoder, which buffers bytes ahead of bufio.Reader
	// (so bufio.Reader.Peek would block on data the decoder already consumed).
	if err := c.skipLeadingWhitespace(); err != nil {
		return nil, err
	}
	head, err := c.reader.Peek(len("content-length"))
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(head) == len("content-length") && strings.EqualFold(string(head), "content-length") {
		return c.readContentLength()
	}
	return c.readNewline()
}

func (c *compatConn) skipLeadingWhitespace() error {
	for {
		b, err := c.reader.Peek(1)
		if err != nil {
			return err
		}
		switch b[0] {
		case ' ', '\t', '\r', '\n':
			if _, err := c.reader.ReadByte(); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (c *compatConn) readContentLength() (jsonrpc.Message, error) {
	c.setFormat(frameContentLength)
	length := -1
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "content-length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("invalid content-length header: %w", err)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("missing content-length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return nil, err
	}
	return jsonrpc.DecodeMessage(body)
}

func (c *compatConn) readNewline() (jsonrpc.Message, error) {
	c.setFormat(frameNewline)
	c.decoderMu.Lock()
	defer c.decoderMu.Unlock()
	if c.decoder == nil {
		c.decoder = newJSONDecoder(c.reader)
	}
	raw, err := c.decoder.Decode()
	if err != nil {
		return nil, err
	}
	return jsonrpc.DecodeMessage(raw)
}

func (c *compatConn) setFormat(format frameFormat) {
	c.formatMu.Lock()
	if !c.formatDetected {
		c.format = format
		c.formatDetected = true
	}
	c.formatMu.Unlock()
}

// jsonDecoder keeps a json.Decoder pinned to the buffered reader so that
// consecutive newline-framed messages can be decoded without losing buffered
// bytes when the decoder is replaced.
type jsonDecoder struct {
	decoder *json.Decoder
}

func newJSONDecoder(reader io.Reader) *jsonDecoder {
	return &jsonDecoder{decoder: json.NewDecoder(reader)}
}

func (d *jsonDecoder) Decode() (json.RawMessage, error) {
	var raw json.RawMessage
	if err := d.decoder.Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}
