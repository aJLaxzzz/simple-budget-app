package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"sort"
	"time"
)

func createTemplateTx(tx *sql.Tx, kind string, snapshot ProjectTemplateSnapshot, op, now string) (ProjectTemplateDTO, error) {
	snapshot, e := normalizeTemplate(snapshot)
	if e != nil {
		return ProjectTemplateDTO{}, e
	}
	id, e := operation()
	if e != nil {
		return ProjectTemplateDTO{}, e
	}
	if _, e = tx.Exec("INSERT INTO project_templates(id,title,template_kind,created_at,updated_at) VALUES(?,?,?,?,?)", id, snapshot.Title, kind, now, now); e != nil {
		return ProjectTemplateDTO{}, e
	}
	if _, e = insertTemplateVersion(tx, id, snapshot, now); e != nil {
		return ProjectTemplateDTO{}, e
	}
	v, e := getTemplateKind(tx, id, "", kind)
	if e == nil {
		e = event(tx, "template", id, "created", op, now, nil, v)
	}
	return v, e
}
func linkTemplate(tx *sql.Tx, task, project *string, t ProjectTemplateDTO, role string, series *string, now string) error {
	_, e := tx.Exec("INSERT INTO entity_template_links(task_id,project_id,template_id,version_id,role,series_id,created_at) VALUES(?,?,?,?,?,?,?)", task, project, t.ID, t.VersionID, role, series, now)
	return e
}
func (s *Service) TaskTemplateCommand(ctx context.Context, id, action string, in TaskTemplateInput) (CommandResult, error) {
	var result CommandResult
	now := stamp(s.now())
	op, e := operation()
	if e != nil {
		return result, e
	}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		result, e = commandOnce(tx, in.RequestID, "task-template/"+id, action, now, in, func() (CommandResult, error) {
			var before ProjectTemplateDTO
			var source *TaskDTO
			if action != "instantiate" && (in.ProjectID != nil || in.PlannedDate != nil) {
				return CommandResult{}, invalid("Контекст даты/проекта допустим только при создании задачи")
			}
			if action != "create" && in.SourceTaskID != "" {
				return CommandResult{}, invalid("Источник допустим только при сохранении нового шаблона")
			}
			if action == "create" {
				if in.SourceTaskID != "" {
					t, e := getTask(tx, in.SourceTaskID)
					if e != nil {
						return CommandResult{}, e
					}
					if e = checkRevision(in.ExpectedRevision, t.RecordMeta); e != nil {
						return CommandResult{}, e
					}
					source = &t
				} else if in.ExpectedRevision != 0 {
					return CommandResult{}, invalid("Версия относится к исходной задаче")
				}
			} else {
				var e error
				before, e = getTemplateKind(tx, id, "", "task")
				if e != nil {
					return CommandResult{}, e
				}
				if e = checkRevision(in.ExpectedRevision, before.RecordMeta); e != nil {
					return CommandResult{}, e
				}
				if before.Archived {
					return CommandResult{}, invalid("Шаблон архивирован")
				}
			}
			switch action {
			case "create", "versions":
				b := in.Snapshot
				if b == nil && source != nil {
					b = &TaskBlueprint{Appearance: Appearance{ColorID: &source.EffectiveColorID, IconID: &source.EffectiveIconID}, Title: source.Title, Description: source.Description, Priority: source.Priority}
				}
				if b == nil {
					return CommandResult{}, invalid("Нужны настройки шаблона")
				}
				snapshot := ProjectTemplateSnapshot{Appearance: b.Appearance, Title: b.Title, Description: b.Description, Tasks: []ProjectTemplateTask{{Appearance: b.Appearance, Key: "task", Title: b.Title, Description: b.Description, Priority: b.Priority}}}
				snapshot, e := normalizeTemplate(snapshot)
				if e != nil {
					return CommandResult{}, e
				}
				var v ProjectTemplateDTO
				if action == "create" {
					v, e = createTemplateTx(tx, "task", snapshot, op, now)
				} else {
					_, e = tx.Exec("UPDATE project_templates SET title=?,revision=revision+1,updated_at=? WHERE id=?", snapshot.Title, now, id)
					if e == nil {
						_, e = insertTemplateVersion(tx, id, snapshot, now)
					}
					if e == nil {
						v, e = getTemplateKind(tx, id, "", "task")
					}
					if e == nil {
						e = event(tx, "template", id, "version_created", op, now, before, v)
					}
				}
				if e != nil {
					return CommandResult{}, e
				}
				if source != nil {
					if e = linkTemplate(tx, &source.ID, nil, v, "saved", nil, now); e != nil {
						return CommandResult{}, e
					}
					if _, e = tx.Exec("UPDATE tasks SET revision=revision+1,updated_at=? WHERE id=?", now, source.ID); e != nil {
						return CommandResult{}, e
					}
					if e = event(tx, "task", source.ID, "saved_as_template", op, now, nil, map[string]any{"template_id": v.ID, "version_id": v.VersionID}); e != nil {
						return CommandResult{}, e
					}
				}
				return CommandResult{Template: &v}, nil
			case "archive":
				if in.Snapshot != nil {
					return CommandResult{}, invalid("Архивирование не меняет содержимое")
				}
				if _, e := tx.Exec("UPDATE project_templates SET archived=1,revision=revision+1,updated_at=? WHERE id=?", now, id); e != nil {
					return CommandResult{}, e
				}
				v, e := getTemplateKind(tx, id, "", "task")
				if e == nil {
					e = event(tx, "template", id, "archived", op, now, before, v)
				}
				return CommandResult{Template: &v}, e
			case "instantiate":
				if in.Snapshot != nil {
					return CommandResult{}, invalid("Используйте сохранённый шаблон")
				}
				b := before.Snapshot.Tasks[0]
				tid, e := operation()
				if e != nil {
					return CommandResult{}, e
				}
				t := TaskDTO{RecordMeta: RecordMeta{ID: tid, Revision: 1, CreatedAt: now, UpdatedAt: now}, Appearance: b.Appearance, Title: b.Title, Description: b.Description, Priority: b.Priority, Status: "planned", ProjectID: in.ProjectID, PlannedDate: in.PlannedDate}
				if e = validateTask(tx, t); e != nil {
					return CommandResult{}, e
				}
				if e = insertTask(tx, t); e != nil {
					return CommandResult{}, e
				}
				if e = linkTemplate(tx, &t.ID, nil, before, "source", nil, now); e != nil {
					return CommandResult{}, e
				}
				if e = event(tx, "task", t.ID, "created_from_template", op, now, nil, map[string]any{"task": t, "template_id": id, "version_id": before.VersionID}); e != nil {
					return CommandResult{}, e
				}
				if e = event(tx, "template", id, "instantiated", op, now, nil, map[string]any{"task_id": t.ID, "version_id": before.VersionID}); e != nil {
					return CommandResult{}, e
				}
				t, e = getTask(tx, t.ID)
				return CommandResult{Task: &t}, e
			}
			return CommandResult{}, invalid("Неизвестная команда шаблона")
		})
		return e
	})
	return result, e
}
func relativeDate(anchor string, date *string) (*int, error) {
	if date == nil {
		return nil, nil
	}
	a, e := time.Parse("2006-01-02", anchor)
	if e != nil {
		return nil, invalid("Укажите дату основы")
	}
	d, e := time.Parse("2006-01-02", *date)
	if e != nil {
		return nil, e
	}
	// UTC civil midnights; durations here measure a difference, never advance a date.
	n := int(d.Sub(a).Hours() / 24)
	if n < -3650 || n > 3650 {
		return nil, invalid("Смещение даты превышает 3650 дней")
	}
	return &n, nil
}
func projectSnapshot(p ProjectDTO, anchor string) (ProjectTemplateSnapshot, error) {
	sort.SliceStable(p.Tasks, func(i, j int) bool { return p.Tasks[i].SortOrder < p.Tasks[j].SortOrder })
	v := ProjectTemplateSnapshot{Appearance: p.Appearance, Title: p.Title, Description: p.Description, Tasks: []ProjectTemplateTask{}}
	var e error
	v.DueOffset, e = relativeDate(anchor, p.DueDate)
	if e != nil {
		return v, e
	}
	for _, t := range p.Tasks {
		plan, e := relativeDate(anchor, t.PlannedDate)
		if e != nil {
			return v, e
		}
		due, e := relativeDate(anchor, t.DueDate)
		if e != nil {
			return v, e
		}
		v.Tasks = append(v.Tasks, ProjectTemplateTask{Appearance: t.Appearance, Key: t.ID, ParentKey: t.ParentTaskID, Title: t.Title, Description: t.Description, Priority: t.Priority, PlannedOffset: plan, DueOffset: due, ScheduledTime: t.ScheduledTime})
	}
	return normalizeTemplate(v)
}
func (s *Service) projectSourcePreviewTx(ctx context.Context, tx *sql.Tx, id string, in ProjectSourceInput) (ProjectSourcePreview, error) {
	var out ProjectSourcePreview
	if in.ProjectMode != "" && in.ProjectMode != "sequential" && in.ProjectMode != "independent" {
		return out, invalid("Выберите режим выполнения")
	}
	if in.ProjectMode != "" && in.Rule != nil && in.FirstDate <= in.AnchorDate {
		return out, invalid("Следующее выполнение должно начинаться позже исходного периода")
	}
	p, e := projectWithTree(tx, id)
	if e != nil {
		return out, e
	}
	if e = checkRevision(in.ExpectedRevision, p.RecordMeta); e != nil {
		return out, e
	}
	if e = managementDate(in.AnchorDate); e != nil {
		return out, e
	}
	if in.Rule != nil && (p.OccurrenceID != nil || p.RepeatSeriesID != nil || p.ArchivedAt != nil) {
		return out, conflict("already_repeating", "Проект уже связан с повторением или архивирован. Откройте существующее повторение.", p)
	}

	if in.IndependentDecision != "" && in.IndependentDecision != "convert" {
		return out, invalid("Неизвестное решение о независимых повторениях")
	}
	seenSeries := map[string]bool{}
	filtered := []TaskDTO{}
	excluded := map[string]bool{}
	for _, t := range p.Tasks {
		if t.SeriesID == nil {
			filtered = append(filtered, t)
			continue
		}
		if in.IndependentDecision != "convert" {
			return out, conflict("double_recurrence", "В проекте есть независимые повторения. Оставьте проект обычным либо явно преобразуйте один выбранный элемент каждого повторения в шаблон.", p)
		}
		if seenSeries[*t.SeriesID] {
			out.Excluded = append(out.Excluded, t)
			excluded[t.ID] = true
			continue
		}
		seenSeries[*t.SeriesID] = true
		filtered = append(filtered, t)
		series, err := getSeries(tx, *t.SeriesID)
		if err != nil {
			return out, err
		}
		conversion, err := s.disablePreviewTx(tx, series.ID, DisableInput{ExpectedRevision: series.Revision, KeepID: t.ID, Future: "delete"})
		if err != nil {
			return out, err
		}
		out.Conversions = append(out.Conversions, conversion)
	}
	// Descendants of omitted occurrences cannot become orphan template roots.
	tree := []TaskDTO{}
	for _, t := range filtered {
		if t.ParentTaskID != nil && excluded[*t.ParentTaskID] {
			out.Excluded = append(out.Excluded, t)
			excluded[t.ID] = true
		} else {
			tree = append(tree, t)
		}
	}
	original := p
	p.Tasks = tree

	snapshot, e := projectSnapshot(p, in.AnchorDate)
	if e != nil {
		return out, e
	}
	if in.Snapshot != nil {
		snapshot, e = normalizeTemplate(*in.Snapshot)
		if e != nil {
			return out, e
		}
	}
	for _, t := range snapshot.Tasks {
		if excluded[t.Key] {
			return out, invalid("Материализованные копии одного повторения нельзя повторно включить в шаблон")
		}
	}
	out.Snapshot = snapshot
	out.Items = []TemplatePreviewCycle{}
	if in.Rule != nil {
		if in.Rule.Mode != recurrence.Schedule {
			return out, invalid("Проекты повторяются только от расписания")
		}
		rule, e := recurrence.NormalizeRule(*in.Rule)
		if e != nil {
			return out, recurrenceAPIError(e)
		}
		if e = managementDate(in.FirstDate); e != nil {
			return out, e
		}
		if e = validDate(in.EndDate); e != nil {
			return out, e
		}
		if in.EndDate != nil && *in.EndDate < in.FirstDate {
			return out, invalid("Окончание серии не может предшествовать первому выполнению")
		}
		v := SeriesDTO{SourceType: "project", AnchorMode: "schedule", ProjectTemplate: &snapshot, Rule: StoredTaskRule{Rule: rule, FirstDate: recurrence.Date(in.FirstDate), Anchor: recurrence.Anchor{Date: recurrence.Date(in.FirstDate), Kind: recurrence.ScheduleAnchor}}}
		for i := int64(0); i < 5; i++ {
			c, _, e := calculateStoredCycle(ctx, tx, v, i, nil)
			if e != nil {
				return out, recurrenceAPIError(e)
			}
			if in.EndDate != nil && string(c.AdjustedDate) > *in.EndDate {
				if i == 0 {
					return out, invalid("Первое выполнение позже окончания")
				}
				break
			}
			project, e := composeProject(snapshot, string(c.AdjustedDate))
			if e != nil {
				return out, e
			}
			out.Items = append(out.Items, TemplatePreviewCycle{Candidate: &c, Project: project})
		}
	} else if in.FirstDate != "" || in.EndDate != nil {
		return out, invalid("Дата следующего выполнения относится к повторению")
	}
	tokenInput := in
	tokenInput.RequestID = ""
	tokenInput.PreviewToken = ""
	out.PreviewToken, e = digest(struct {
		Input       ProjectSourceInput
		Source      ProjectDTO
		Snapshot    ProjectTemplateSnapshot
		Items       []TemplatePreviewCycle
		Conversions []DisablePreview
	}{tokenInput, original, snapshot, out.Items, out.Conversions})
	return out, e
}
func (s *Service) TaskTemplate(ctx context.Context, id string) (ProjectTemplateDTO, error) {
	var v ProjectTemplateDTO
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error { var e error; v, e = getTemplateKind(tx, id, "", "task"); return e })
	return v, e
}
func (s *Service) TaskTemplates(ctx context.Context, archived bool, offset int) ([]ProjectTemplateDTO, error) {
	if offset < 0 {
		return nil, invalid("Некорректная пагинация")
	}
	items := []ProjectTemplateDTO{}
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		rows, e := tx.Query("SELECT id FROM project_templates WHERE template_kind='task' AND archived=? ORDER BY created_at,id LIMIT 50 OFFSET ?", archived, offset)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			v, e := getTemplateKind(tx, id, "", "task")
			if e != nil {
				return e
			}
			items = append(items, v)
		}
		return nil
	})
	return items, e
}
func (s *Service) ProjectTemplatePreview(ctx context.Context, id string, in ProjectSourceInput) (ProjectSourcePreview, error) {
	var v ProjectSourcePreview
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error { var e error; v, e = s.projectSourcePreviewTx(ctx, tx, id, in); return e })
	return v, recurrenceAPIError(e)
}
func (s *Service) SaveProjectTemplate(ctx context.Context, id string, in ProjectSourceInput) (CommandResult, error) {
	now := stamp(s.now())
	op, e := operation()
	if e != nil {
		return CommandResult{}, e
	}
	var result CommandResult
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		result, e = commandOnce(tx, in.RequestID, "project-template/"+id, "save", now, in, func() (CommandResult, error) {
			preview, e := s.projectSourcePreviewTx(ctx, tx, id, in)
			if e != nil {
				return CommandResult{}, e
			}
			if in.PreviewToken == "" || in.PreviewToken != preview.PreviewToken {
				return CommandResult{}, conflict("stale_preview", "Состав проекта или настройки изменились. Повторите предпросмотр.", nil)
			}
			for _, conversion := range preview.Conversions {
				keep := ""
				for _, row := range conversion.Rows {
					if row.Decision == "detach" {
						for _, t := range preview.Snapshot.Tasks {
							if row.ID == t.Key {
								keep = row.ID
							}
						}
					}
				}
				if keep == "" {
					return CommandResult{}, invalid("Сохраняемый элемент повторения должен остаться в составе шаблона")
				}
				_, e = s.disableTx(ctx, tx, conversion.Series.ID, DisableInput{ExpectedRevision: conversion.Series.Revision, KeepID: keep, Future: "delete", PreviewToken: conversion.PreviewToken, Confirm: true, RequestID: in.RequestID + ":" + conversion.Series.ID})
				if e != nil {
					return CommandResult{}, e
				}
			}
			t, e := createTemplateTx(tx, "project", preview.Snapshot, op, now)
			if e != nil {
				return CommandResult{}, e
			}
			out := CommandResult{Template: &t}
			var seriesID *string
			if in.Rule != nil {
				rule, e := recurrence.NormalizeRule(*in.Rule)
				if e != nil {
					return out, e
				}
				out, e = s.createProjectSeriesTx(ctx, tx, CreateSeriesInput{SourceType: "project", RequestID: in.RequestID, TemplateID: t.ID, TemplateVersionID: t.VersionID, ExpectedRevision: t.Revision, FirstDate: in.FirstDate, Rule: rule, EndDate: in.EndDate}, rule, op, now, id)
				if e != nil {
					return out, e
				}
				out.Template = &t
				seriesID = &out.Series.ID
				if in.ProjectMode != "" {
					if _, e = tx.Exec(`UPDATE project_templates SET internal=1 WHERE id=?`, t.ID); e != nil {
						return out, e
					}
					var current any
					if in.ProjectMode == "sequential" {
						current = id
					}
					if _, e = tx.Exec(`INSERT INTO project_repeat_state(series_id,mode,source_project_id,current_project_id) VALUES(?,?,?,?)`, *seriesID, in.ProjectMode, id, current); e != nil {
						return out, e
					}
				}
			}
			if e = linkTemplate(tx, nil, &id, t, "saved", seriesID, now); e != nil {
				return out, e
			}
			if _, e = tx.Exec("UPDATE projects SET revision=revision+1,updated_at=? WHERE id=?", now, id); e != nil {
				return out, e
			}
			if e = event(tx, "project", id, "saved_as_template", op, now, nil, map[string]any{"template_id": t.ID, "version_id": t.VersionID, "series_id": seriesID, "original_project": "independent"}); e != nil {
				return out, e
			}
			p, e := projectWithTree(tx, id)
			out.Project = &p
			return out, e
		})
		return e
	})
	return result, recurrenceAPIError(e)
}
