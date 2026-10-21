package boardpb_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bobmcallan/satelle/internal/hosted/boardpb"
)

// TestBoardDescriptor proves the generated descriptor carries the contract
// api/board.proto publishes: the service, its three RPCs, every message field
// with its number and every enum value (sty_73c9aa10). satelle-server copies the
// proto and tests the same facts, so a renumbered field fails here first.
func TestBoardDescriptor(t *testing.T) {
	fd := boardpb.File_board_proto
	if got := string(fd.Package()); got != "satelle.board.v1" {
		t.Fatalf("package = %q", got)
	}
	if fd.Services().Len() != 1 {
		t.Fatalf("services = %d, want 1", fd.Services().Len())
	}
	svc := fd.Services().Get(0)
	if svc.Name() != "Board" {
		t.Fatalf("service = %q", svc.Name())
	}
	wantRPC := map[string][2]string{
		"Post":  {"PostRequest", "PostResponse"},
		"Fetch": {"FetchRequest", "FetchResponse"},
		"Mark":  {"MarkRequest", "MarkResponse"},
	}
	if svc.Methods().Len() != len(wantRPC) {
		t.Fatalf("rpcs = %d, want %d", svc.Methods().Len(), len(wantRPC))
	}
	for i := 0; i < svc.Methods().Len(); i++ {
		m := svc.Methods().Get(i)
		want, ok := wantRPC[string(m.Name())]
		if !ok {
			t.Fatalf("unexpected rpc %q", m.Name())
		}
		if string(m.Input().Name()) != want[0] || string(m.Output().Name()) != want[1] {
			t.Errorf("rpc %s = %s -> %s, want %s -> %s", m.Name(), m.Input().Name(), m.Output().Name(), want[0], want[1])
		}
		if m.IsStreamingClient() || m.IsStreamingServer() {
			t.Errorf("rpc %s must be unary", m.Name())
		}
	}

	wantFields := map[string]map[string]string{
		"To":            {"kind": "1:enum", "value": "2:string"},
		"Message":       {"id": "1:string", "project": "2:string", "story_id": "3:string", "from_session_id": "4:string", "from_session_name": "5:string", "from_location": "6:string", "to": "7:message", "body": "8:string", "in_reply_to": "9:string", "answer_id": "10:string", "created_at": "11:message", "updated_at": "12:message", "deadline": "13:message", "state": "14:enum"},
		"PostRequest":   {"message": "1:message"},
		"PostResponse":  {"message": "1:message"},
		"FetchRequest":  {"project": "1:string", "page_size": "2:int32", "page_token": "3:string", "since_token": "4:string", "session_ids": "5:string", "roles": "6:string", "story_ids": "7:string", "states": "8:enum"},
		"FetchResponse": {"messages": "1:message", "next_page_token": "2:string", "sync_token": "3:string"},
		"MarkRequest":   {"id": "1:string", "state": "2:enum", "answer_id": "3:string"},
		"MarkResponse":  {"message": "1:message"},
	}
	if fd.Messages().Len() != len(wantFields) {
		t.Fatalf("messages = %d, want %d", fd.Messages().Len(), len(wantFields))
	}
	for name, fields := range wantFields {
		md := fd.Messages().ByName(protoreflect.Name(name))
		if md == nil {
			t.Fatalf("message %s missing", name)
		}
		if md.Fields().Len() != len(fields) {
			t.Errorf("%s has %d fields, want %d", name, md.Fields().Len(), len(fields))
		}
		for fname, spec := range fields {
			f := md.Fields().ByName(protoreflect.Name(fname))
			if f == nil {
				t.Errorf("%s.%s missing", name, fname)
				continue
			}
			if got := strconv.Itoa(int(f.Number())) + ":" + f.Kind().String(); got != spec {
				t.Errorf("%s.%s = %s, want %s", name, fname, got, spec)
			}
		}
	}
	for _, rep := range []struct{ msg, field string }{
		{"FetchRequest", "session_ids"}, {"FetchRequest", "roles"}, {"FetchRequest", "story_ids"},
		{"FetchRequest", "states"}, {"FetchResponse", "messages"},
	} {
		if !fd.Messages().ByName(protoreflect.Name(rep.msg)).Fields().ByName(protoreflect.Name(rep.field)).IsList() {
			t.Errorf("%s.%s must be repeated", rep.msg, rep.field)
		}
	}
	for _, ts := range []string{"created_at", "updated_at", "deadline"} {
		f := fd.Messages().ByName("Message").Fields().ByName(protoreflect.Name(ts))
		if f.Message() == nil || f.Message().FullName() != "google.protobuf.Timestamp" {
			t.Errorf("Message.%s must be a google.protobuf.Timestamp", ts)
		}
	}

	checkEnum(t, fd.Enums().ByName("State"), map[string]int{
		"STATE_UNSPECIFIED": 0, "STATE_WAITING": 1, "STATE_DELIVERED": 2, "STATE_ANSWERED": 3, "STATE_UNDELIVERABLE": 4,
	})
	checkEnum(t, fd.Messages().ByName("To").Enums().ByName("Kind"), map[string]int{
		"KIND_UNSPECIFIED": 0, "KIND_STORY": 1, "KIND_ROLE": 2, "KIND_SESSION": 3, "KIND_ALL": 4,
	})
}

func checkEnum(t *testing.T, ed protoreflect.EnumDescriptor, want map[string]int) {
	t.Helper()
	if ed == nil {
		t.Fatal("enum missing")
	}
	if ed.Values().Len() != len(want) {
		t.Errorf("%s has %d values, want %d", ed.Name(), ed.Values().Len(), len(want))
	}
	for name, num := range want {
		v := ed.Values().ByName(protoreflect.Name(name))
		if v == nil || int(v.Number()) != num {
			t.Errorf("%s.%s != %d", ed.Name(), name, num)
		}
	}
}

// TestBoardDescriptorMatchesProto reads api/board.proto and compares every
// message field, enum value and rpc it declares with the generated descriptor,
// so the descriptor cannot drift from the published text (a stale regeneration
// fails here, not in satelle-server).
func TestBoardDescriptorMatchesProto(t *testing.T) {
	b, err := os.ReadFile("../../../api/board.proto")
	if err != nil {
		t.Fatalf("read board.proto: %v", err)
	}
	fd := boardpb.File_board_proto
	var (
		scope []string
		kinds []string
		field = regexp.MustCompile(`^(?:repeated\s+)?[\w.]+\s+(\w+)\s*=\s*(\d+);`)
		enumv = regexp.MustCompile(`^(\w+)\s*=\s*(\d+);`)
		open  = regexp.MustCompile(`^(message|enum|service)\s+(\w+)\s*\{`)
		rpc   = regexp.MustCompile(`^rpc\s+(\w+)\(`)
		seen  int
	)
	for _, raw := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(raw)
		if i := strings.Index(l, "//"); i >= 0 {
			l = strings.TrimSpace(l[:i])
		}
		switch {
		case open.MatchString(l):
			m := open.FindStringSubmatch(l)
			scope, kinds = append(scope, m[2]), append(kinds, m[1])
		case l == "}":
			scope, kinds = scope[:len(scope)-1], kinds[:len(kinds)-1]
		case len(kinds) > 0 && kinds[len(kinds)-1] == "service" && rpc.MatchString(l):
			if fd.Services().ByName("Board").Methods().ByName(protoreflect.Name(rpc.FindStringSubmatch(l)[1])) == nil {
				t.Errorf("rpc %q is in board.proto but not the descriptor", l)
			}
			seen++
		case len(kinds) > 0 && kinds[len(kinds)-1] == "message" && field.MatchString(l):
			m := field.FindStringSubmatch(l)
			md := fd.Messages().ByName(protoreflect.Name(scope[0]))
			f := md.Fields().ByName(protoreflect.Name(m[1]))
			if f == nil || strconv.Itoa(int(f.Number())) != m[2] {
				t.Errorf("%s.%s = %s is in board.proto but not the descriptor", scope[0], m[1], m[2])
			}
			seen++
		case len(kinds) > 0 && kinds[len(kinds)-1] == "enum" && enumv.MatchString(l):
			m := enumv.FindStringSubmatch(l)
			var ed protoreflect.EnumDescriptor
			if len(scope) == 1 {
				ed = fd.Enums().ByName(protoreflect.Name(scope[0]))
			} else {
				ed = fd.Messages().ByName(protoreflect.Name(scope[0])).Enums().ByName(protoreflect.Name(scope[1]))
			}
			v := ed.Values().ByName(protoreflect.Name(m[1]))
			if v == nil || strconv.Itoa(int(v.Number())) != m[2] {
				t.Errorf("%s.%s = %s is in board.proto but not the descriptor", ed.Name(), m[1], m[2])
			}
			seen++
		}
	}
	// The proto declares 3 rpcs, 33 fields and 10 enum values: a parse that
	// matched next to nothing must not pass.
	if seen < 40 {
		t.Fatalf("matched only %d declarations in board.proto", seen)
	}
}
