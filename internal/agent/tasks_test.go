package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTaskListReadsEveryBackendsTaskTool(t *testing.T) {
	args := func(s string) map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	want := []Task{
		{Text: "Read the code", Status: TaskDone},
		{Text: "Fixing the bug", Status: TaskInProgress},
		{Text: "Run the tests", Status: TaskPending},
	}
	cases := map[string]struct {
		tool string
		args string
	}{
		"claude TodoWrite": {"TodoWrite", `{"todos":[
			{"content":"Read the code","status":"completed","activeForm":"Reading the code"},
			{"content":"Fix the bug","status":"in_progress","activeForm":"Fixing the bug"},
			{"content":"Run the tests","status":"pending","activeForm":"Running the tests"}]}`},
		"opencode todowrite": {"todowrite", `{"todos":[
			{"content":"Read the code","status":"completed","id":"1"},
			{"content":"Fixing the bug","status":"in_progress","id":"2"},
			{"content":"Run the tests","status":"pending","id":"3"}]}`},
		"copilot update_todo": {"update_todo", `{"todos":"- [x] Read the code\n- [~] Fixing the bug\n- [ ] Run the tests\nnot a task"}`},
	}
	for name, c := range cases {
		got, ok := TaskList(c.tool, args(c.args))
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: TaskList = %#v, %v", name, got, ok)
		}
	}
	if _, ok := TaskList("Bash", args(`{"command":"ls"}`)); ok {
		t.Error("an ordinary tool read as a task list")
	}
	if got, ok := TaskList("TodoWrite", args(`{"todos":[]}`)); !ok || len(got) != 0 {
		t.Errorf("a cleared list = %#v, %v; want an empty list that counts", got, ok)
	}
}

func TestHeadlessTasksFrame(t *testing.T) {
	ev, ok := decodeHeadless([]byte(`{"type":"tasks","tasks":[{"text":"a","status":"done"},{"text":"b","status":"in_progress"}]}`))
	want := []Task{{Text: "a", Status: TaskDone}, {Text: "b", Status: TaskInProgress}}
	if !ok || ev.Kind != EventTasks || !reflect.DeepEqual(ev.Tasks, want) {
		t.Fatalf("tasks frame = %#v, %v", ev, ok)
	}
}
