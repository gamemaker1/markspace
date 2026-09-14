package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAIResultElapsedIsMilliseconds(t *testing.T) {
	res := AIResult{Text: "ok", Provider: "local", Elapsed: 1500 * time.Millisecond,
		Sources: []AISource{{Kind: ContextNote, Title: "Plan"}}}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"elapsedMs":1500`) {
		t.Fatalf("elapsedMs not in milliseconds: %s", s)
	}
	if strings.Count(s, "elapsed") != 1 {
		t.Fatalf("elapsed appears more than once: %s", s)
	}
	for _, want := range []string{`"text":"ok"`, `"provider":"local"`, `"sources":[`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
	// A pointer marshals the same way.
	ptr, _ := json.Marshal(&res)
	if string(ptr) != s {
		t.Fatalf("pointer encoding differs: %s", ptr)
	}
}
