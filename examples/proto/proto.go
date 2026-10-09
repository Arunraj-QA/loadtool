// Package proto embeds the .proto files the gRPC examples and tests use,
// so the test service and the demo API share one copy.
package proto

import _ "embed"

// Greeter is greeter.proto.
//
//go:embed greeter.proto
var Greeter string
