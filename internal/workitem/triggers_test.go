package workitem

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseTriggers(t *testing.T) {
	got, err := ParseTriggers("nightly=schedule:24h, release=count:awaiting_merge>=3")
	if err != nil {
		t.Fatal(err)
	}
	want := []TriggerDecl{
		{Name: "nightly", Kind: TriggerSchedule, Every: 24 * time.Hour},
		{Name: "release", Kind: TriggerCount, Status: "awaiting_merge", AtLeast: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if again, _ := ParseTriggers(FormatTriggers(got)); !reflect.DeepEqual(again, want) {
		t.Fatalf("format/parse round trip: %+v", again)
	}
	if d, err := ParseTriggers(""); d != nil || err != nil {
		t.Fatalf("empty line = %v, %v", d, err)
	}
	if tag := want[1].Tag("tsk_release"); tag != "trigger:tsk_release:release" {
		t.Errorf("tag = %q", tag)
	}
}

func TestParseTriggersMalformed(t *testing.T) {
	for line, frag := range map[string]string{
		"a=cron:* * * * *":            "must be schedule or count",
		"a=schedule:daily":            "positive duration",
		"a=schedule:-1h":              "positive duration",
		"a=count:done>3":              "<status>>=<n>",
		"a=count:done<=3":             "<status>>=<n>",
		"a=count:done>=0":             "at least 1",
		"a=schedule:1h, a=count:x>=1": "declared twice",
		"Bad=schedule:1h":             "must match",
		"a":                           "name=schedule",
		"a=schedule":                  "name=schedule",
	} {
		_, err := ParseTriggers(line)
		if err == nil || !strings.Contains(err.Error(), frag) {
			t.Errorf("%q: err = %v, want %q", line, err, frag)
		}
	}
}

func TestMapInputsKeepsOnlyDeclaredValues(t *testing.T) {
	decls, _ := ParseInputDecls("trigger:string, story_id:string")
	got := MapInputs(decls, map[string]string{InputTrigger: "release", InputTriggerSource: "count", InputStoryID: ""})
	if want := map[string]string{"trigger": "release"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (undeclared and empty values dropped)", got, want)
	}
}

// A task's triggers round-trip through its markdown file and the store.
func TestTriggersRoundTripMarkdownAndStore(t *testing.T) {
	trig, _ := ParseTriggers("nightly=schedule:1h30m, release=count:awaiting_merge>=2")
	it := Item{ID: "tsk_1", Kind: KindTask, Status: "backlog", Title: "T", Triggers: trig}
	parsed, err := Parse(Marshal(it))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.Triggers, trig) {
		t.Errorf("markdown round trip = %+v, want %+v", parsed.Triggers, trig)
	}

	s := openStore(t)
	ctx := context.Background()
	made, err := s.Create(ctx, CreateInput{Kind: KindTask, Title: "T", Triggers: trig}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, made.ID)
	if err != nil || !reflect.DeepEqual(got.Triggers, trig) {
		t.Fatalf("created row triggers = %+v (%v), want %+v", got.Triggers, err, trig)
	}
	got.Triggers = trig[:1]
	if _, err := s.Upsert(ctx, got, time.Now()); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Get(ctx, made.ID); !reflect.DeepEqual(again.Triggers, trig[:1]) {
		t.Errorf("upserted row triggers = %+v, want %+v", again.Triggers, trig[:1])
	}

	bare, _ := Parse(Marshal(Item{ID: "tsk_2", Kind: KindTask, Status: "backlog", Title: "T"}))
	if bare.Triggers != nil || strings.Contains(string(Marshal(bare)), "triggers:") {
		t.Errorf("a task without triggers gained them: %+v", bare.Triggers)
	}
}
