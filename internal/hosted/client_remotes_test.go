package hosted

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fireAnswering fires trigger "nightly" against a server that answers code and
// body.
func fireAnswering(t *testing.T, code int, body string) error {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	store := &memStore{cred: Credential{ServerURL: ts.URL, AccessToken: "t", RefreshToken: "r"}, set: true}
	_, err := NewClient(ts.URL, store, ts.Client()).FireRemote(context.Background(), "rem_1", "", "nightly")
	return err
}

func TestFireRemoteWakeFailed(t *testing.T) {
	err := fireAnswering(t, http.StatusBadGateway, `{"error":"wake_failed","message":"wake failed","trigger_id":"fire_7"}`)
	var failed *WakeFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("a 502 wake failure must be a *WakeFailedError, got %v", err)
	}
	if failed.Trigger != "nightly" || failed.TriggerID != "fire_7" || failed.Reason != "wake failed" {
		t.Errorf("got %+v", failed)
	}
	for _, want := range []string{"nightly", "wake failed", "fire recorded as fire_7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("message must name the outcome, not the status: %v", err)
	}
}

func TestFireRemoteWakeFailedWithoutRecordID(t *testing.T) {
	// The server may answer the bare sentence with no id: still a wake failure.
	err := fireAnswering(t, http.StatusBadGateway, `{"error":"wake failed"}`)
	var failed *WakeFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "wake failed") || !strings.HasSuffix(err.Error(), "fire recorded") {
		t.Errorf("the output must still say the fire was recorded: %v", err)
	}
	if strings.Contains(err.Error(), "fire recorded as") {
		t.Errorf("no id was given, none may be claimed: %v", err)
	}
}

func TestFireRemoteOtherBadGatewayStaysGeneric(t *testing.T) {
	for name, body := range map[string]string{
		"html":      `<html>502 Bad Gateway</html>`,
		"other":     `{"error":"bad_gateway"}`,
		"emptybody": ``,
	} {
		t.Run(name, func(t *testing.T) {
			err := fireAnswering(t, http.StatusBadGateway, body)
			var failed *WakeFailedError
			if err == nil || errors.As(err, &failed) {
				t.Fatalf("a 502 that is not a wake failure must stay a generic error, got %v", err)
			}
			if !strings.Contains(err.Error(), "HTTP 502") {
				t.Errorf("generic error must name the status: %v", err)
			}
		})
	}
}

// retireAnswering retires remote "rmt_1" against a server that answers code.
func retireAnswering(t *testing.T, code int) error {
	t.Helper()
	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(code)
	}))
	t.Cleanup(ts.Close)
	store := &memStore{cred: Credential{ServerURL: ts.URL, AccessToken: "t", RefreshToken: "r"}, set: true}
	err := NewClient(ts.URL, store, ts.Client()).RetireRemote(context.Background(), "rmt_1")
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/remotes/rmt_1" {
		t.Errorf("RetireRemote sent %s %s", gotMethod, gotPath)
	}
	return err
}

func TestRetireRemoteTypesItsRefusals(t *testing.T) {
	if err := retireAnswering(t, http.StatusNoContent); err != nil {
		t.Fatalf("a 204 retires: %v", err)
	}
	var nf *RemoteNotFoundError
	if err := retireAnswering(t, http.StatusNotFound); !errors.As(err, &nf) || nf.RemoteID != "rmt_1" {
		t.Errorf("a 404 must be a *RemoteNotFoundError naming the remote, got %v", err)
	}
	var fb *RemoteForbiddenError
	if err := retireAnswering(t, http.StatusForbidden); !errors.As(err, &fb) || fb.RemoteID != "rmt_1" {
		t.Errorf("a 403 must be a *RemoteForbiddenError naming the remote, got %v", err)
	}
	if err := retireAnswering(t, http.StatusInternalServerError); err == nil || errors.As(err, &nf) || errors.As(err, &fb) {
		t.Errorf("a 500 stays a generic error, got %v", err)
	}
}

// A 404 on a fire (a POST) keeps the generic error: the typing is for a retire.
func TestFireNotFoundStaysGeneric(t *testing.T) {
	err := fireAnswering(t, http.StatusNotFound, "")
	var nf *RemoteNotFoundError
	if err == nil || errors.As(err, &nf) {
		t.Errorf("a 404 on a fire must stay a generic error, got %v", err)
	}
}
