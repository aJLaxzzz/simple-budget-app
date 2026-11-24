package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
)

// This command joins the ordinary edit and the immutable future composition in
// one transaction. Neither half is committed if a revision or dependency fails.
type ProjectTaskScopeInput struct {
	ExpectedRevision       int64            `json:"expected_revision"`
	ExpectedSeriesRevision int64            `json:"expected_series_revision"`
	ExpectedTaskRevision   int64            `json:"expected_task_revision"`
	Scope                  string           `json:"scope"`
	Action                 string           `json:"action"`
	TaskID                 string           `json:"task_id,omitempty"`
	ParentID               string           `json:"parent_id,omitempty"`
	Patch                  json.RawMessage  `json:"patch,omitempty"`
	TaskIDs                []string         `json:"task_ids,omitempty"`
	RelatedRevisions       map[string]int64 `json:"related_revisions,omitempty"`
	Confirm                bool             `json:"confirm"`
	PreviewToken           string           `json:"preview_token"`
	RequestID              string           `json:"request_id"`
}
type ProjectTaskScopePreview struct {
	Project      ProjectDTO         `json:"project"`
	Scope        string             `json:"scope"`
	Action       string             `json:"action"`
	Before       *TaskDTO           `json:"before,omitempty"`
	After        *TaskDTO           `json:"after,omitempty"`
	Future       CompositionPreview `json:"future"`
	Deletions    []DeletionPreview  `json:"deletions"`
	PreviewToken string             `json:"preview_token"`
	key          string
	patch        TaskInput
}

func scopeTaskKeys(tx *sql.Tx, p ProjectDTO) (map[string]string, error) {
	if p.OccurrenceID != nil {
		return projectKeys(tx, p.ID)
	}
	keys := map[string]string{}
	for _, t := range p.Tasks {
		keys[t.ID] = t.ID
	}
	return keys, nil
}
func scopePatch(raw json.RawMessage) (TaskInput, error) {
	var in TaskInput
	if len(raw) == 0 {
		return in, nil
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		return in, invalid("Некорректные поля задачи")
	}
	allowed := map[string]bool{"title": true, "description": true, "priority": true, "planned_date": true, "due_date": true, "scheduled_time": true, "color_id": true, "icon_id": true}
	for k := range fields {
		if !allowed[k] {
			return in, invalid("Область изменения не поддерживает поле: " + k)
		}
	}
	e := json.Unmarshal(raw, &in)
	return in, e
}
func (s *Service) projectTaskScopePreview(ctx context.Context, tx *sql.Tx, id string, in ProjectTaskScopeInput) (ProjectTaskScopePreview, error) {
	out := ProjectTaskScopePreview{Scope: in.Scope, Action: in.Action, Deletions: []DeletionPreview{}}
	if in.Scope != "this_and_following" && in.Scope != "following" {
		return out, invalid("Выберите это и следующие либо только следующие выполнения")
	}
	if in.Action != "edit" && in.Action != "add" && in.Action != "remove" && in.Action != "order" {
		return out, invalid("Неизвестное изменение состава")
	}
	p, e := projectWithTree(tx, id)
	if e != nil {
		return out, e
	}
	out.Project = p
	if e = checkRevision(in.ExpectedRevision, p.RecordMeta); e != nil {
		return out, e
	}
	if p.RepeatSeriesID == nil || p.ArchivedAt != nil {
		return out, invalid("Требуется доступный повторяющийся проект")
	}
	v, e := getSeries(tx, *p.RepeatSeriesID)
	if e != nil {
		return out, e
	}
	if e = checkRevision(in.ExpectedSeriesRevision, v.RecordMeta); e != nil {
		return out, e
	}
	if v.State == "disabled" || v.State == "archived" {
		return out, invalid("Повторение прекращено")
	}
	if p.StartDate == nil {
		return out, invalid("Для относительных дат укажите начало выбранного проекта")
	}
	keys, e := scopeTaskKeys(tx, p)
	if e != nil {
		return out, e
	}
	reverse := map[string]string{}
	for key, id := range keys {
		reverse[id] = key
	}
	patch, e := scopePatch(in.Patch)
	if e != nil {
		return out, e
	}
	out.patch = patch
	raw, _ := json.Marshal(v.ProjectTemplate)
	var snapshot ProjectTemplateSnapshot
	if e = json.Unmarshal(raw, &snapshot); e != nil {
		return out, e
	}
	local := TaskDTO{ProjectID: &p.ID, Status: "planned", Priority: "normal"}
	if in.Action == "edit" || in.Action == "remove" {
		local, e = getTask(tx, in.TaskID)
		if e != nil {
			return out, e
		}
		if local.ProjectID == nil || *local.ProjectID != p.ID || local.SeriesID != nil {
			return out, invalid("Выберите дочернюю задачу этого проекта без отдельного повторения")
		}
		if e = checkRevision(in.ExpectedTaskRevision, local.RecordMeta); e != nil {
			return out, e
		}
		out.Before = &local
		out.key = reverse[local.ID]
		if out.key == "" {
			out.key = local.ID
		}
	}
	if in.Action == "add" {
		clean := in
		clean.PreviewToken = ""
		clean.RequestID = ""
		clean.Confirm = false
		hash, e := digest(clean)
		if e != nil {
			return out, e
		}
		out.key = hash[:32]
		local.ID = out.key
		if in.ParentID != "" {
			parent, e := getTask(tx, in.ParentID)
			if e != nil {
				return out, e
			}
			if parent.ProjectID == nil || *parent.ProjectID != p.ID || parent.ParentTaskID != nil {
				return out, invalid("Выберите родительскую задачу проекта")
			}
			local.ParentTaskID = &in.ParentID
		}
	}
	if in.Action == "edit" || in.Action == "add" {
		next := local
		if e = applyTask(patch, &next); e != nil {
			return out, e
		}
		if e = validateTask(tx, next); e != nil {
			return out, e
		}
		if e = validateAppearance(tx, next.Appearance); e != nil {
			return out, e
		}
		out.After = &next
		index := -1
		for i, t := range snapshot.Tasks {
			if t.Key == out.key {
				index = i
				break
			}
		}
		if index < 0 {
			var parent *string
			if next.ParentTaskID != nil {
				k := reverse[*next.ParentTaskID]
				if k == "" {
					return out, invalid("Сначала добавьте родительскую задачу в следующие выполнения")
				}
				parent = &k
			}
			plan, e := relativeDate(*p.StartDate, next.PlannedDate)
			if e != nil {
				return out, e
			}
			due, e := relativeDate(*p.StartDate, next.DueDate)
			if e != nil {
				return out, e
			}
			snapshot.Tasks = append(snapshot.Tasks, ProjectTemplateTask{Key: out.key, ParentKey: parent, Title: next.Title, Description: next.Description, Priority: next.Priority, Appearance: next.Appearance, PlannedOffset: plan, DueOffset: due, ScheduledTime: next.ScheduledTime})
			index = len(snapshot.Tasks) - 1
		}
		t := &snapshot.Tasks[index]
		if patch.Title.Set {
			t.Title = next.Title
		}
		if patch.Description.Set {
			t.Description = next.Description
		}
		if patch.Priority.Set {
			t.Priority = next.Priority
		}
		if patch.ColorID.Set {
			t.ColorID = next.ColorID
		}
		if patch.IconID.Set {
			t.IconID = next.IconID
		}
		if patch.ScheduledTime.Set {
			t.ScheduledTime = next.ScheduledTime
		}
		if patch.PlannedDate.Set {
			t.PlannedOffset, e = relativeDate(*p.StartDate, next.PlannedDate)
			if e != nil {
				return out, e
			}
		}
		if patch.DueDate.Set {
			t.DueOffset, e = relativeDate(*p.StartDate, next.DueDate)
			if e != nil {
				return out, e
			}
		}
	}
	if in.Action == "remove" {
		remaining := []ProjectTemplateTask{}
		for _, t := range snapshot.Tasks {
			if t.Key != out.key && (t.ParentKey == nil || *t.ParentKey != out.key) {
				remaining = append(remaining, t)
			}
		}
		snapshot.Tasks = remaining
	}
	if in.Action == "order" {
		if len(in.TaskIDs) == 0 {
			return out, invalid("Выберите задачи одного уровня")
		}
		ordered := []ProjectTemplateTask{}
		seen := map[string]bool{}
		var parent *string
		for i, id := range in.TaskIDs {
			t, e := getTask(tx, id)
			if e != nil {
				return out, e
			}
			if t.ProjectID == nil || *t.ProjectID != p.ID || t.SeriesID != nil || seen[id] {
				return out, invalid("Некорректный состав порядка")
			}
			seen[id] = true
			if e = checkRevision(in.RelatedRevisions[id], t.RecordMeta); e != nil {
				return out, e
			}
			if i == 0 {
				parent = t.ParentTaskID
			} else if !reflect.DeepEqual(parent, t.ParentTaskID) {
				return out, invalid("Порядок меняется внутри одного родителя")
			}
			found := false
			for _, x := range snapshot.Tasks {
				if x.Key == reverse[id] {
					ordered = append(ordered, x)
					found = true
					break
				}
			}
			if !found {
				return out, invalid("Сначала добавьте задачу в следующие выполнения")
			}
		}
		at := 0
		for i, t := range snapshot.Tasks {
			if seen[keys[t.Key]] {
				snapshot.Tasks[i] = ordered[at]
				at++
			}
		}
	}
	snapshot, e = normalizeTemplate(snapshot)
	if e != nil {
		return out, e
	}
	// Existing composition checks protect whole manually changed future projects.
	boundary := int64(-1)
	if p.OccurrenceID != nil {
		o, e := getOccurrence(tx, *p.OccurrenceID)
		if e != nil {
			return out, e
		}
		boundary = o.CycleIndex
	}
	future, e := compositionPreview(ctx, tx, v.ID, CompositionInput{ExpectedRevision: v.Revision, Snapshot: &snapshot, RemovedTasksDecision: "cancel", AfterCycle: &boundary})
	if e != nil {
		return out, e
	}
	out.Future = future
	out.Future.Rows = []CompositionRow{}
	for _, row := range future.Rows {
		if row.Before.ID == p.ID {
			continue
		}
		o, e := getOccurrence(tx, *row.Before.OccurrenceID)
		if e != nil {
			return out, e
		}
		if o.CycleIndex > boundary {
			out.Future.Rows = append(out.Future.Rows, row)
		}
	}
	// Selected edit is explicit: preserve unrelated manual content, but do not
	// silently rewrite closed/started targets or their work sessions.
	if in.Scope == "this_and_following" {
		if !active(p.Status) {
			return out, invalid("Выбранный проект закрыт. Выберите только следующие выполнения")
		}
		targets := []TaskDTO{}
		if out.Before != nil {
			targets = append(targets, *out.Before)
			if in.Action == "remove" {
				for _, t := range p.Tasks {
					if t.ParentTaskID != nil && *t.ParentTaskID == in.TaskID {
						targets = append(targets, t)
					}
				}
			}
		}
		if in.Action == "order" {
			for _, t := range p.Tasks {
				for _, id := range in.TaskIDs {
					if t.ID == id {
						targets = append(targets, t)
					}
				}
			}
		}
		for _, t := range targets {
			var sessions int
			if e = tx.QueryRow("SELECT count(*) FROM sessions WHERE task_id=?", t.ID).Scan(&sessions); e != nil {
				return out, e
			}
			if t.Status != "planned" || sessions > 0 {
				return out, invalid("Выбранная задача начата, закрыта или имеет периоды работы. Сохраните её и выберите только следующие выполнения")
			}
		}
	}
	if in.Action == "remove" {
		ids := []string{}
		if in.Scope == "this_and_following" {
			ids = append(ids, in.TaskID)
		}
		for _, r := range out.Future.Rows {
			if r.After == nil {
				continue
			}
			ks, e := projectKeys(tx, r.Before.ID)
			if e != nil {
				return out, e
			}
			if tid := ks[out.key]; tid != "" {
				ids = append(ids, tid)
			}
		}
		for _, id := range ids {
			d, e := deletionPlanTx(tx, "tasks", id)
			if e != nil {
				return out, e
			}
			if len(d.Blockers) > 0 {
				return out, conflict("deletion_dependencies", "Удаление заблокировано зависимостями", d.DeletionPreview)
			}
			out.Deletions = append(out.Deletions, d.DeletionPreview)
		}
	}
	clean := in
	clean.PreviewToken = ""
	clean.RequestID = ""
	clean.Confirm = false
	out.PreviewToken, e = digest(struct {
		Input   ProjectTaskScopeInput
		Preview ProjectTaskScopePreview
	}{clean, out})
	return out, e
}
func projectTaskScopeRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in ProjectTaskScopeInput
	if e := strictPlannerInput(w, r, &in, "expected_revision expected_series_revision expected_task_revision scope action task_id parent_id patch task_ids related_revisions confirm preview_token request_id"); e != nil {
		return 0, nil, e
	}
	var out any
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		if action == "task-change-preview" {
			p, e := s.projectTaskScopePreview(r.Context(), tx, id, in)
			out = p
			return e
		}
		result, e := commandOnce(tx, in.RequestID, "project/"+id, "scoped_task_change", stamp(s.now()), in, func() (CommandResult, error) {
			p, e := s.projectTaskScopePreview(r.Context(), tx, id, in)
			if e != nil {
				return CommandResult{}, e
			}
			if in.PreviewToken == "" || p.PreviewToken != in.PreviewToken {
				return CommandResult{}, conflict("preview_changed", "Данные изменились. Повторите предпросмотр; черновик сохранён", p)
			}
			if in.Action == "remove" && !in.Confirm {
				return CommandResult{}, invalid("Подтвердите окончательное удаление перечисленных задач")
			}
			now := stamp(s.now())
			result, e := s.applyCompositionPreviewTx(r.Context(), tx, *p.Project.RepeatSeriesID, p.Future, "cancel", in.RequestID, now)
			if e != nil {
				return result, e
			}
			if in.Scope == "this_and_following" {
				switch in.Action {
				case "edit":
					input := p.patch
					input.ExpectedRevision = in.ExpectedTaskRevision
					var t TaskDTO
					if e = s.changeTaskTx(r.Context(), tx, in.TaskID, "", input, in.RequestID, now, s.now(), &t, taskChangeOptions{}); e != nil {
						return result, e
					}
					result.Task = &t
				case "add":
					t := *p.After
					t.RecordMeta = RecordMeta{ID: p.key, Revision: 1, CreatedAt: now, UpdatedAt: now}
					if e = insertTask(tx, t); e != nil {
						return result, e
					}
					if e = event(tx, "task", t.ID, "created", in.RequestID, now, nil, t); e != nil {
						return result, e
					}
					result.Task = &t
					if p.Project.OccurrenceID != nil {
						keys, e := projectKeys(tx, id)
						if e != nil {
							return result, e
						}
						keys[p.key] = t.ID
						raw, _ := json.Marshal(keys)
						if _, e = tx.Exec("UPDATE project_instances SET task_keys_json=? WHERE project_id=?", string(raw), id); e != nil {
							return result, e
						}
					}
				case "order":
					for i, tid := range in.TaskIDs {
						t, e := getTask(tx, tid)
						if e != nil {
							return result, e
						}
						if _, e = tx.Exec("UPDATE tasks SET sort_order=?,revision=revision+1,updated_at=? WHERE id=?", i, now, tid); e != nil {
							return result, e
						}
						if e = event(tx, "task", tid, "reordered", in.RequestID, now, t, map[string]any{"sort_order": i}); e != nil {
							return result, e
						}
					}
				}
			}
			// Physical deletion reuses the same dependency engine as ordinary deletion.
			// Each child command remains within this outer SQLite transaction.
			for _, d := range p.Deletions {
				fresh, e := deletionPlanTx(tx, "tasks", d.ID)
				if e != nil {
					return result, e
				}
				childRequest, err := digest([]string{"scoped-task-delete", in.RequestID, d.ID})
				if err != nil {
					return result, err
				}
				_, e = s.deleteForeverTx(r.Context(), tx, "tasks", d.ID, DeletionInput{ExpectedRevision: fresh.Revision, PreviewToken: fresh.PreviewToken, Confirm: true, RequestID: childRequest})
				if e != nil {
					return result, e
				}
			}
			if in.Action == "remove" {
				for _, row := range p.Future.Rows {
					if row.After != nil {
						keys, err := projectKeys(tx, row.Before.ID)
						if err != nil {
							return result, err
						}
						if err = projectBaseline(tx, row.Before.ID, keys); err != nil {
							return result, err
						}
					}
				}
			}
			if in.Scope == "this_and_following" {
				if _, e = tx.Exec("UPDATE projects SET revision=revision+1,updated_at=? WHERE id=?", now, id); e != nil {
					return result, e
				}
			}
			project, e := projectWithTree(tx, id)
			result.Project = &project
			return result, e
		})
		out = result
		return e
	})
	return 200, out, e
}
