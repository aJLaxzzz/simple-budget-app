package planner

import "testing"

func TestUXActiveTasksAndParentContext(t *testing.T) {
	_, _, h := seriesFixture(t)
	p := value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": "Project"}, 201))
	parent := task(t, h, map[string]any{"title": "Parent", "project_id": p.ID})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": parent.ID})
	apiCall(t, h, "POST", "/tasks/"+parent.ID+"/complete", map[string]any{"expected_revision": parent.Revision, "completed_date": "2026-11-15", "children_decision": "keep_open", "related_revisions": map[string]int64{child.ID: child.Revision}}, 200)
	active := value[struct {
		Items []TaskDTO `json:"items"`
	}](t, apiCall(t, h, "GET", "/tasks?status=active", nil, 200))
	if len(active.Items) != 1 || active.Items[0].ID != child.ID || active.Items[0].ParentTitle == nil || *active.Items[0].ParentTitle != "Parent" {
		t.Fatal(active)
	}
	all := value[struct {
		Items []TaskDTO `json:"items"`
	}](t, apiCall(t, h, "GET", "/tasks", nil, 200))
	if len(all.Items) != 2 {
		t.Fatal(all)
	}
	projects := value[struct {
		Items []ProjectDTO `json:"items"`
	}](t, apiCall(t, h, "GET", "/projects", nil, 200))
	if projects.Items[0].TaskCount != 2 {
		t.Fatal(projects)
	}
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Bad", "parent_title": "Injection"}, 400)
}
