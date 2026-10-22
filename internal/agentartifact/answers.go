package agentartifact

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// The answers a performer gives a finding it was shown (sty_f948d345).
const (
	AnswerAdopted  = "adopted"
	AnswerRebutted = "rebutted"
)

// Answer is a performer's reply to one blocking finding of the edge it is
// re-presenting: Finding is the id from the payload's findings_to_answer, Answer
// is adopted or rebutted, Evidence is the rebuttal's grounds (or what changed).
// How strictly a gate holds the reply to evidence is the repo's rubric; this
// package only decodes it.
type Answer struct {
	Finding  string `json:"finding"`
	Answer   string `json:"answer"`
	Evidence string `json:"evidence,omitempty"`
}

// DecodeAnswers extracts the `answers` list from a final agent response: the
// last object carrying an `answers` array whose keys are all report keys, so it
// rides beside the artifact envelope or the changes report. Absent returns nil.
// A malformed list, an unknown field, an empty finding or an answer other than
// adopted/rebutted is an error: a malformed report is never silently dropped.
func DecodeAnswers(out []byte) ([]Answer, error) {
	var raw json.RawMessage
	for _, obj := range jsonObjects(out) {
		var envelope map[string]json.RawMessage
		if json.Unmarshal(obj, &envelope) != nil {
			continue
		}
		list, ok := envelope["answers"]
		if !ok || !isReport(envelope) {
			continue
		}
		if t := bytes.TrimSpace(list); len(t) == 0 || t[0] != '[' {
			continue
		}
		raw = list
	}
	if raw == nil {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("answers: expected a list: %w", err)
	}
	var answers []Answer
	for i, item := range items {
		dec := json.NewDecoder(bytes.NewReader(item))
		dec.DisallowUnknownFields()
		var a Answer
		if err := dec.Decode(&a); err != nil {
			return nil, fmt.Errorf("answers[%d]: %w", i, err)
		}
		if a.Finding == "" {
			return nil, fmt.Errorf("answers[%d]: finding is required", i)
		}
		if a.Answer != AnswerAdopted && a.Answer != AnswerRebutted {
			return nil, fmt.Errorf("answers[%d]: answer %q must be %q or %q", i, a.Answer, AnswerAdopted, AnswerRebutted)
		}
		answers = append(answers, a)
	}
	return answers, nil
}

// FindingAnswersRecord is the part of a performer's agent_invocation row that
// records the blocking findings it was offered and what it answered
// (sty_f948d345): the one decode of those two keys, shared by the gate payload
// and the cost view so neither restates the key names.
type FindingAnswersRecord struct {
	Offered []string `json:"findings_offered"`
	Answers []Answer `json:"finding_answers"`
}

// MatchAnswers pairs answers with the finding ids that were offered: answered
// holds the first answer to each offered id, in answer order; an answer to an id
// that was not offered, or a second answer to one already answered, is dropped.
// missing lists the offered ids with no answer, in offered order.
func MatchAnswers(offered []string, answers []Answer) (answered []Answer, missing []string) {
	asked := map[string]bool{}
	for _, id := range offered {
		asked[id] = true
	}
	done := map[string]bool{}
	for _, a := range answers {
		if asked[a.Finding] && !done[a.Finding] {
			done[a.Finding] = true
			answered = append(answered, a)
		}
	}
	for _, id := range offered {
		if !done[id] {
			missing = append(missing, id)
		}
	}
	return answered, missing
}
