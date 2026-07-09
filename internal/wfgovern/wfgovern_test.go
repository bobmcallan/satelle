package wfgovern_test

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func names(ds []docindex.Doc) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Name
	}
	return out
}

func TestOrderedWorkflowsPriority(t *testing.T) {
	sysWild := docindex.Doc{Name: "satelle-baseline-workflow", Embedded: true,
		Body: "---\nscope: system\napplies_to: [\"*\"]\n---\n"}
	repoWild := docindex.Doc{Name: "satelle-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"*\"]\n---\n"}
	repoSpec := docindex.Doc{Name: "satelle-web-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"web\"]\n---\n"}
	all := []docindex.Doc{sysWild, repoWild, repoSpec}

	got := wfgovern.OrderedWorkflows(all, "")
	if len(got) != 2 || got[0].Name != "satelle-workflow" || got[1].Name != "satelle-baseline-workflow" {
		t.Fatalf("wildcard order = %v, want [satelle-workflow, satelle-baseline-workflow]", names(got))
	}
	got = wfgovern.OrderedWorkflows(all, "web")
	if got[0].Name != "satelle-web-workflow" {
		t.Fatalf("category-web head = %s, want satelle-web-workflow", got[0].Name)
	}
}

func TestExecutionResolvesToTaskExecutionWorkflow(t *testing.T) {
	storyWild := docindex.Doc{Name: "satelle-project-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"*\"]\n---\n"}
	taskExec := docindex.Doc{Name: "satelle-task-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"execution\", \"task\"]\n---\n"}
	all := []docindex.Doc{storyWild, taskExec}

	if got := wfgovern.WorkflowCategory(workitem.Item{Kind: workitem.KindExecution}); got != "execution" {
		t.Fatalf("WorkflowCategory(execution) = %q, want \"execution\"", got)
	}
	if got := wfgovern.WorkflowCategory(workitem.Item{Kind: workitem.KindTask, Category: "substrate"}); got != "task" {
		t.Fatalf("WorkflowCategory(task) = %q, want \"task\"", got)
	}
	got := wfgovern.OrderedWorkflows(all, wfgovern.WorkflowCategory(workitem.Item{Kind: workitem.KindExecution}))
	if len(got) == 0 || got[0].Name != "satelle-task-workflow" {
		t.Fatalf("execution head = %v, want satelle-task-workflow", names(got))
	}
	tk := wfgovern.OrderedWorkflows(all, wfgovern.WorkflowCategory(workitem.Item{Kind: workitem.KindTask, Category: "substrate"}))
	if len(tk) == 0 || tk[0].Name != "satelle-task-workflow" {
		t.Fatalf("task-header head = %v, want satelle-task-workflow", names(tk))
	}
	sk := wfgovern.OrderedWorkflows(all, wfgovern.WorkflowCategory(workitem.Item{Kind: workitem.KindStory, Category: "feature"}))
	if len(sk) == 0 || sk[0].Name != "satelle-project-workflow" {
		t.Fatalf("story head = %v, want satelle-project-workflow", names(sk))
	}
}

func TestOrderedWorkflowsParentCategories(t *testing.T) {
	repoWild := docindex.Doc{Name: "satelle-project-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"*\"]\n---\n"}
	repoParent := docindex.Doc{Name: "satelle-parent-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"epic-parent\", \"parent\"]\n---\n"}
	all := []docindex.Doc{repoWild, repoParent}

	for _, cat := range []string{"epic-parent", "parent"} {
		got := wfgovern.OrderedWorkflows(all, cat)
		if len(got) == 0 || got[0].Name != "satelle-parent-workflow" {
			t.Errorf("category %q head = %v, want satelle-parent-workflow first", cat, names(got))
		}
	}
	if got := wfgovern.OrderedWorkflows(all, "feature"); len(got) == 0 || got[0].Name != "satelle-project-workflow" {
		t.Errorf("category feature head = %v, want satelle-project-workflow", names(got))
	}
}

func TestStampedWorkflowName(t *testing.T) {
	if got := wfgovern.StampedWorkflowName(workitem.Item{Tags: []string{"a", "workflow:my-wf", "b"}}); got != "my-wf" {
		t.Errorf("StampedWorkflowName = %q, want my-wf", got)
	}
	if got := wfgovern.StampedWorkflowName(workitem.Item{Tags: []string{"a", "b"}}); got != "" {
		t.Errorf("un-stamped item = %q, want empty", got)
	}
}

func TestGoverningWorkflowStampWins(t *testing.T) {
	wfFeature := docindex.Doc{Name: "wf-feature", Body: "---\napplies_to: [\"feature\"]\n---\n"}
	wfChore := docindex.Doc{Name: "wf-chore", Body: "---\napplies_to: [\"chore\"]\n---\n"}
	all := []docindex.Doc{wfFeature, wfChore}

	// Stamp overrides category.
	doc, ok := wfgovern.GoverningWorkflow(all, workitem.Item{
		Kind: workitem.KindStory, Category: "feature", Tags: []string{"workflow:wf-chore"},
	})
	if !ok || doc.Name != "wf-chore" {
		t.Fatalf("stamped govern = %v ok=%v, want wf-chore", doc.Name, ok)
	}
	// Unstamped resolves by category.
	doc, ok = wfgovern.GoverningWorkflow(all, workitem.Item{
		Kind: workitem.KindStory, Category: "feature",
	})
	if !ok || doc.Name != "wf-feature" {
		t.Fatalf("category govern = %v ok=%v, want wf-feature", doc.Name, ok)
	}
}

// TestEntryStateNotHardcodedBacklog proves AC2: the entry state is the Mdiamond
// / Start node whatever it is named — not a "backlog" literal.
func TestEntryStateNotHardcodedBacklog(t *testing.T) {
	body := "---\nname: triage-wf\napplies_to: [\"*\"]\n---\n\n```dot\n" +
		"digraph w {\n" +
		"  triage      [shape=Mdiamond]\n" +
		"  in_progress [agent=executor]\n" +
		"  done        [shape=Msquare]\n" +
		"  triage -> in_progress -> done\n" +
		"}\n```\n"
	spec, ok := wfdot.Parse(body)
	if !ok {
		t.Fatal("parse failed")
	}
	if got := spec.Start(); got != "triage" {
		t.Fatalf("Start() = %q, want triage (not hardcoded backlog)", got)
	}
}

func TestFrontmatterList(t *testing.T) {
	inline := wfgovern.FrontmatterList("---\napplies_to: [\"*\", web]\n---\nx", "applies_to")
	if len(inline) != 2 || inline[0] != "*" || inline[1] != "web" {
		t.Fatalf("inline = %v", inline)
	}
	block := wfgovern.FrontmatterList("---\nname: w\napplies_to:\n  - web\n  - infra\nother: y\n---\nx", "applies_to")
	if len(block) != 2 || block[0] != "web" || block[1] != "infra" {
		t.Fatalf("block = %v", block)
	}
	if wfgovern.FrontmatterList("no frontmatter", "applies_to") != nil {
		t.Fatal("expected nil for no frontmatter")
	}
}
