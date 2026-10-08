package protocol_test

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
	"github.com/Arunraj-QA/loadtool/internal/protocol/protocoltest"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		err  error
		want protocol.ErrorCode
	}{
		{nil, protocol.CodeNone},
		{&net.DNSError{Err: "no such host", Name: "x.invalid"}, protocol.CodeDNS},
		{&net.OpError{Op: "dial", Err: errors.New("connection refused")}, protocol.CodeDial},
		{fmt.Errorf("handshake: %w", x509.UnknownAuthorityError{}), protocol.CodeTLS},
		{fmt.Errorf("read: %w", os.ErrDeadlineExceeded), protocol.CodeTimeout},
		{context.DeadlineExceeded, protocol.CodeTimeout},
		{fmt.Errorf("read frame: %w", io.ErrUnexpectedEOF), protocol.CodeClosed},
		{net.ErrClosed, protocol.CodeClosed},
		{errors.New("bad frame"), protocol.CodeProtocol},
	}
	for _, tt := range tests {
		if got := protocol.Classify(tt.err); got != tt.want {
			t.Errorf("Classify(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

func recordVU(t *testing.T) (*protocoltest.VU, *metrics.Families, protocol.Families) {
	t.Helper()
	fams, err := metrics.NewFamilies([]metrics.Def{
		{Name: "ws_op_duration", Kind: metrics.Trend},
		{Name: "ws_ops", Kind: metrics.Counter},
		{Name: "ws_op_failed", Kind: metrics.Rate},
	})
	if err != nil {
		t.Fatal(err)
	}
	vu := protocoltest.NewVU(1)
	vu.Recorder().UseFamilies(fams)
	return vu, fams, protocol.Families{Duration: 0, Count: 1, Failed: 2}
}

func TestRecord(t *testing.T) {
	vu, fams, f := recordVU(t)
	if code := protocol.Record(vu, f, protocol.Outcome{Duration: 3 * time.Millisecond, Sent: true}); code != "" {
		t.Errorf("success: code %q", code)
	}
	if code := protocol.Record(vu, f, protocol.Outcome{Duration: time.Millisecond, Err: io.EOF, Sent: true}); code != protocol.CodeClosed {
		t.Errorf("transport failure: code %q", code)
	}
	if code := protocol.Record(vu, f, protocol.Outcome{Duration: time.Millisecond, Code: protocol.CodeServer, Sent: true}); code != protocol.CodeServer {
		t.Errorf("server failure: code %q", code)
	}
	if code := protocol.Record(vu, f, protocol.Outcome{Err: errors.New("bad url")}); code != protocol.CodeInvalid {
		t.Errorf("unsent: code %q", code)
	}
	s := fams.Summarize()
	// The unsent operation has no duration; every operation is counted
	// and three of the four failed.
	if s[0].Count != 3 || s[0].Failed != 2 || s[1].Count != 4 || s[2].Trues != 3 || s[2].Count != 4 {
		t.Errorf("recorded %+v", s)
	}
}

// Operations cut short by the end of the test, and calls in top-level
// code, are not recorded.
func TestRecordSkipsEndedAndInitContexts(t *testing.T) {
	vu, fams, f := recordVU(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	vu.SetContext(ctx)
	protocol.Record(vu, f, protocol.Outcome{Err: context.Canceled, Sent: true})
	vu.SetContext(nil)
	protocol.Record(vu, f, protocol.Outcome{Sent: true})
	for _, s := range fams.Summarize() {
		if s.Count != 0 {
			t.Errorf("%s recorded %d", s.Name, s.Count)
		}
	}
}

func TestFamilyIDs(t *testing.T) {
	_, fams, _ := recordVU(t)
	env := protocol.RunEnv{Families: fams}
	ids, err := protocol.FamilyIDs(env, "ws_op_failed", "ws_ops")
	if err != nil || ids[0] != 2 || ids[1] != 1 {
		t.Errorf("FamilyIDs = %v, %v", ids, err)
	}
	if _, err := protocol.FamilyIDs(env, "ws_nope"); err == nil {
		t.Error("want an error for an undeclared family")
	}
}
