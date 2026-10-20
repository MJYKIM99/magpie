package gateway

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// stepShape lists a Responses input's items by type, a reasoning item with
// its reasoning_text and a message with its role.
func stepShape(input []map[string]any) string {
	var got []string
	for _, it := range input {
		s, _ := it["type"].(string)
		switch s {
		case "reasoning":
			content, _ := it["content"].([]any)
			for _, c := range content {
				if m, _ := c.(map[string]any); m["type"] == "reasoning_text" {
					s += fmt.Sprintf("=%q", m["text"])
				}
			}
		case "message":
			s += ":" + fmt.Sprint(it["role"])
		}
		got = append(got, s)
	}
	return strings.Join(got, ",")
}

// TestDeepSeekStepSaysBeforeItsCall (#1822, Codex → OpenCode Go's DeepSeek;
// #1592 and #1687 on DeepSeek's own API): a step of Codex's history that
// said something and then made a call, with no reasoning item (another
// model's turn), was sent [message, reasoning " ", call]: magpie put the
// reasoning it made up right before the call. DeepSeek reads a step as one
// assistant message whose thinking is the item it starts with, so it
// refused the request with "The `reasoning_text` in the thinking mode must
// be passed back to the API", while a step with only a call was taken. Each
// step now leads with its reasoning: its own moved ahead of what it said,
// else a space.
func TestDeepSeekStepSaysBeforeItsCall(t *testing.T) {
	fresh(t)
	up := &deepseekResponses{}
	srv := httptest.NewServer(up)
	defer srv.Close()
	if err := provider.Save(provider.Provider{ID: "opencode-go", Name: "OpenCode Go", Key: "k", Models: []string{"deepseek-v4.1-flash"}, Responses: srv.URL + "/zen/go/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "deepseek", Name: "DeepSeek", Preset: "deepseek", Key: "k", Models: []string{"deepseek-flash"}, Responses: srv.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{ID: "codex-brain", Name: "codex-brain", Members: []string{"opencode-go/deepseek-v4.1-flash"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	user := `{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the login bug"}]}`
	said := func(text string) string {
		return `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}`
	}
	call := func(id string) string {
		return `{"type":"function_call","call_id":"` + id + `","name":"shell","arguments":"{\"command\":[\"ls\"]}"},
		  {"type":"function_call_output","call_id":"` + id + `","output":"main.go"}`
	}
	sealed := `{"type":"reasoning","id":"rs_0d1f","summary":[],"content":null,"encrypted_content":"gAAAAABpZm9vYmFy"}`
	summed := `{"type":"reasoning","id":"rs_2","summary":[{"type":"summary_text","text":"Run the tests."}],"content":null,"encrypted_content":null}`
	for _, c := range []struct{ name, input, want string }{
		{"the reporter's: said, then called, no reasoning",
			user + `,` + sealed + `,` + call("c1") + `,` + said("I'll run the tests.") + `,` + call("c2"),
			`message:user,reasoning=" ",function_call,function_call_output,reasoning=" ",message:assistant,function_call,function_call_output`},
		{"said, reasoning, called",
			user + `,` + said("Looking.") + `,` + summed + `,` + call("c1"),
			`message:user,reasoning="Run the tests.",message:assistant,function_call,function_call_output`},
		{"said after a tool output",
			user + `,` + call("c1") + `,` + said("It passes."),
			`message:user,reasoning=" ",function_call,function_call_output,reasoning=" ",message:assistant`},
		{"a step that leads with its reasoning",
			user + `,` + summed + `,` + said("Looking.") + `,` + call("c1"),
			`message:user,reasoning="Run the tests.",message:assistant,function_call,function_call_output`},
	} {
		for _, model := range []string{"opencode-go/deepseek-v4.1-flash", "deepseek/deepseek-flash", "group/codex-brain"} {
			for _, stream := range []string{"true", "false"} {
				code, b := post(t, "/v1/responses", `{"model":"`+model+`","stream":`+stream+`,"store":false,"reasoning":{"effort":"high","summary":"auto"},"include":["reasoning.encrypted_content"],
		  "tools":[{"type":"function","name":"shell","parameters":{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}}}}}],"input":[`+c.input+`]}`)
				if code != 200 {
					t.Errorf("%s, %s, stream %s: %d %s", c.name, model, stream, code, b)
					continue
				}
				if got := stepShape(up.last()); got != c.want {
					t.Errorf("%s, %s: sent\n%s\nwant\n%s", c.name, model, got, c.want)
				}
				for _, it := range up.last() {
					if it["id"] == "rs_0d1f" && it["encrypted_content"] != "gAAAAABpZm9vYmFy" {
						t.Errorf("%s: the sealed item lost its encrypted_content: %v", c.name, it)
					}
				}
			}
		}
	}
}

// withReasoningText leaves a body whose steps all lead with reasoning text
// byte for byte, and a reply to the user (no call, no tool output before
// it) without reasoning, as DeepSeek takes it.
func TestWithReasoningTextLedStepsUnchanged(t *testing.T) {
	for _, b := range []string{
		`{"model":"deepseek-flash","input":[{"type":"message","role":"user","content":"hi"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello!"}]},{"type":"message","role":"user","content":"ls"}]}`,
		`{"model":"deepseek-flash",  "input":[{"type":"message","role":"user","content":"ls"},{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"x"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Looking."}]},{"type":"function_call","call_id":"c1","name":"sh","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"ok"},{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"y"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}]}`,
	} {
		if got := string(withReasoningText([]byte(b))); got != b {
			t.Errorf("%s\nbecame %s", b, got)
		}
	}
}

// Translated to DeepSeek's Responses API (an agent of another protocol, or
// Codex's web_search), an assistant turn that said something before its
// call goes with its reasoning first too (#1822).
func TestTranslatedDeepSeekStepLeadsWithReasoning(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Looking."}]},
		  {"type":"function_call","call_id":"c1","name":"sh","arguments":"{}"}`,
			`message:user,reasoning=" ",message:assistant,function_call,function_call_output`},
		{`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Looking."}]},
		  {"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"Need ls."}]},
		  {"type":"function_call","call_id":"c1","name":"sh","arguments":"{}"}`,
			`message:user,reasoning="Need ls.",message:assistant,function_call,function_call_output`},
	} {
		r, err := parseResponses([]byte(`{"model":"m","input":[{"type":"message","role":"user","content":"ls"},` + c.input + `,
		  {"type":"function_call_output","call_id":"c1","output":"ok"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		var q struct {
			Input []map[string]any `json:"input"`
		}
		json.Unmarshal(buildResponses(r, "deepseek-flash", "api.deepseek.com", false), &q)
		if got := stepShape(q.Input); got != c.want {
			t.Errorf("sent %s\nwant %s", got, c.want)
		}
		if b := buildResponses(r, "gpt-5.4", "relay.example", false); strings.Contains(string(b), `"reasoning_text"`) {
			t.Errorf("gpt-5.4 was given reasoning: %s", b)
		}
	}
}
