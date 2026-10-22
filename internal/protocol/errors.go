package protocol

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
)

// ErrorCode is a normalized error category (ADR-016). Results carry it as
// error_code; "" means no error.
type ErrorCode string

const (
	CodeNone ErrorCode = ""
	// CodeDNS: the host name did not resolve.
	CodeDNS ErrorCode = "dns"
	// CodeDial: the connection was refused or the host unreachable.
	CodeDial ErrorCode = "dial"
	// CodeTLS: a TLS or certificate failure.
	CodeTLS ErrorCode = "tls"
	// CodeTimeout: no complete answer in time (not the end of the test).
	CodeTimeout ErrorCode = "timeout"
	// CodeProtocol: a malformed or unexpected protocol exchange, such as
	// a failed WebSocket handshake.
	CodeProtocol ErrorCode = "protocol"
	// CodeServer: the other side answered with an application error,
	// such as an abnormal WebSocket close code.
	CodeServer ErrorCode = "server"
	// CodeClosed: the connection closed while the operation was in
	// progress.
	CodeClosed ErrorCode = "closed"
	// CodeInvalid: the request could not be built, so it was never sent.
	CodeInvalid ErrorCode = "invalid"
)

// Classify maps a transport error to a category. Application errors are
// the module's to map (to CodeServer); errors Classify does not recognize
// are CodeProtocol. nil is CodeNone.
//
// A cancellation caused by the end of the test is not classified here:
// Record does not record it at all.
func Classify(err error) ErrorCode {
	if err == nil {
		return CodeNone
	}
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalidCert x509.CertificateInvalidError
	var recordErr tls.RecordHeaderError
	var alert tls.AlertError
	var opErr *net.OpError
	var netErr net.Error
	switch {
	case errors.As(err, &dnsErr):
		return CodeDNS
	case errors.As(err, &certErr), errors.As(err, &unknownAuth), errors.As(err, &hostErr),
		errors.As(err, &invalidCert), errors.As(err, &recordErr), errors.As(err, &alert):
		return CodeTLS
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded),
		errors.As(err, &netErr) && netErr.Timeout():
		return CodeTimeout
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return CodeDial
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed),
		errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED), errors.Is(err, syscall.EPIPE):
		return CodeClosed
	}
	return CodeProtocol
}
