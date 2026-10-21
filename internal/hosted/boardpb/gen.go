// Package boardpb is the generated message-board gRPC client.
//
// The published contract is api/board.proto (go_package api). Generation
// remaps that file into this package so internal/hosted can speak Board without
// importing api/. Nothing in the runtime imports this package yet: the board is
// local until a hosted binding posts to it.
//
// It has its own buf template so that regenerating it can never touch syncpb:
// from the repo root, buf generate --template buf.gen.board.yaml
package boardpb
