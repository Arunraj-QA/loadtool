package runner

import (
	"github.com/Arunraj-QA/loadtool/internal/protocol"
	"github.com/Arunraj-QA/loadtool/internal/protocols/grpc"
	"github.com/Arunraj-QA/loadtool/internal/protocols/ws"
)

// modules are the protocol modules scripts can import (ADR-014, ADR-018).
// This is the only place that lists them.
var modules = []protocol.Module{
	ws.Module{},
	grpc.Module{},
}
