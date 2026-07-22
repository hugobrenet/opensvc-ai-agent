package mcpclient

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
)

const maxMCPResponseBodyBytes int64 = 4 << 20

var errMCPResponseBodyTooLarge = errors.New("MCP response body exceeds its limit")

type responseLimitTransport struct {
	base     http.RoundTripper
	maxBytes int64
}

func (t responseLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.Body == nil {
		return response, nil
	}
	if response.ContentLength > t.maxBytes {
		_ = response.Body.Close()
		return nil, responseBodyTooLargeError(t.maxBytes)
	}
	response.Body = &limitedResponseBody{
		body:      response.Body,
		remaining: t.maxBytes + 1,
		maxBytes:  t.maxBytes,
	}
	return response, nil
}

type limitedResponseBody struct {
	body      io.ReadCloser
	remaining int64
	maxBytes  int64
	closeOnce sync.Once
	closeErr  error
}

func (b *limitedResponseBody) Read(buffer []byte) (int, error) {
	if b.remaining <= 0 {
		_ = b.Close()
		return 0, responseBodyTooLargeError(b.maxBytes)
	}
	if int64(len(buffer)) > b.remaining {
		buffer = buffer[:b.remaining]
	}
	n, err := b.body.Read(buffer)
	b.remaining -= int64(n)
	if b.remaining == 0 {
		_ = b.Close()
		return n, responseBodyTooLargeError(b.maxBytes)
	}
	return n, err
}

func (b *limitedResponseBody) Close() error {
	b.closeOnce.Do(func() {
		b.closeErr = b.body.Close()
	})
	return b.closeErr
}

func responseBodyTooLargeError(maxBytes int64) error {
	return fmt.Errorf("%w of %d bytes", errMCPResponseBodyTooLarge, maxBytes)
}
