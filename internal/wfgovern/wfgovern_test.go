package wfgovern_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
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

func TestStartIsTheDeclaredEntryState(t *testing.T) {
	// Start() must read the lifecycle, never assume "backlog": a repo may name its
	// entry state anything. Stated as a Spec literal now that the DOT front end is
	// retired (sty_d953c5d8).
	spec := wfdot.Spec{
		States: []wfdot.State{
			{Name: "triage", Shape: "Mdiamond"},
			{Name: "in_progress", Agent: "executor"},
			{Name: "done", Shape: "Msquare"},
		},
		Transitions: []wfdot.Transition{
			{From: "triage", To: "in_progress"},
			{From: "in_progress", To: "done"},
		},
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

// TestOrderedWorkflowsCaseInsensitive: applies_to casing must not silently miss
// a legal category (the plain-== trap closed by sty_b2315e17 AC3).
func TestOrderedWorkflowsCaseInsensitive(t *testing.T) {
	sub := docindex.Doc{Name: "satelle-substrate-workflow", Embedded: true,
		Body: "---\napplies_to: [\"Substrate\"]\n---\n"}
	wild := docindex.Doc{Name: "satelle-baseline-workflow", Embedded: true,
		Body: "---\napplies_to: [\"*\"]\n---\n"}
	all := []docindex.Doc{sub, wild}
	got := wfgovern.OrderedWorkflows(all, "substrate")
	if len(got) == 0 || got[0].Name != "satelle-substrate-workflow" {
		t.Fatalf("category substrate vs applies_to Substrate head = %v, want substrate workflow", names(got))
	}
}

// TestEveryDefaultCategoryResolvesAWorkflow: every shipped default category
// resolves a governing workflow; epic-parent/parent and substrate beat wildcard.
func TestEveryDefaultCategoryResolvesAWorkflow(t *testing.T) {
	parent := docindex.Doc{Name: "satelle-parent-workflow", Embedded: true,
		Body: "---\napplies_to: [\"epic-parent\", \"parent\"]\n---\n"}
	substrate := docindex.Doc{Name: "satelle-substrate-workflow", Embedded: true,
		Body: "---\napplies_to: [\"substrate\"]\n---\n"}
	baseline := docindex.Doc{Name: "satelle-baseline-workflow", Embedded: true,
		Body: "---\napplies_to: [\"*\"]\n---\n"}
	task := docindex.Doc{Name: "satelle-task-workflow", Embedded: true,
		Body: "---\napplies_to: [\"execution\", \"task\"]\n---\n"}
	all := []docindex.Doc{parent, substrate, baseline, task}

	list := config.EmbeddedCategories()
	if len(list) == 0 {
		t.Fatal("embedded categories empty")
	}
	// task/execution are kinds, not story categories.
	for _, ban := range []string{"task", "execution"} {
		for _, c := range list {
			if strings.EqualFold(c, ban) {
				t.Errorf("category list must not include kind %q", ban)
			}
		}
	}
	for _, cat := range list {
		got := wfgovern.OrderedWorkflows(all, cat)
		if len(got) == 0 {
			t.Errorf("category %q resolves no workflow", cat)
			continue
		}
		switch strings.ToLower(cat) {
		case "epic-parent", "parent":
			if got[0].Name != "satelle-parent-workflow" {
				t.Errorf("%q head = %s, want parent workflow", cat, got[0].Name)
			}
		case "substrate":
			if got[0].Name != "satelle-substrate-workflow" {
				t.Errorf("substrate head = %s, want substrate workflow", got[0].Name)
			}
		default:
			if got[0].Name != "satelle-baseline-workflow" {
				t.Errorf("%q head = %s, want baseline/wildcard", cat, got[0].Name)
			}
		}
	}
	// Kinds still resolve the task workflow.
	for _, kind := range []string{"task", "execution"} {
		got := wfgovern.OrderedWorkflows(all, kind)
		if len(got) == 0 || got[0].Name != "satelle-task-workflow" {
			t.Errorf("kind %q head = %v, want task workflow", kind, names(got))
		}
	}
}
