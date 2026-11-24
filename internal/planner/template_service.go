package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"strings"
	"time"
)

func normalizeTemplate(v ProjectTemplateSnapshot) (ProjectTemplateSnapshot, error) {
	v.Tasks = append([]ProjectTemplateTask(nil), v.Tasks...)
	v.Title = strings.TrimSpace(v.Title)
	if e := validateTitle(v.Title); e != nil {
		return v, e
	}
	if len(v.Description) > 50000 || len(v.Tasks) > 500 {
		return v, invalid("Описание до 50000 байт, дерево до 500 задач")
	}
	offsets := []*int{v.DueOffset}
	keys := map[string]int{}
	for i := range v.Tasks {
		t := &v.Tasks[i]
		t.Title = strings.TrimSpace(t.Title)
		if e := validateTitle(t.Title); e != nil {
			return v, e
		}
		if len(t.Description) > 50000 || len(t.Key) == 0 || len(t.Key) > 100 {
			return v, invalid("Нужны уникальный key до 100 байт и описание до 50000 байт")
		}
		if _, ok := keys[t.Key]; ok {
			return v, invalid("Повторяющийся key")
		}
		keys[t.Key] = i
		if t.Priority == "" {
			t.Priority = "normal"
		}
		if t.Priority != "low" && t.Priority != "normal" && t.Priority != "high" {
			return v, invalid("Некорректный приоритет")
		}
		offsets = append(offsets, t.PlannedOffset, t.DueOffset)
		if t.ScheduledTime != nil {
			p, e := time.Parse("15:04", *t.ScheduledTime)
			if e != nil || p.Format("15:04") != *t.ScheduledTime || t.PlannedOffset == nil {
				return v, invalid("Время HH:MM требует planned_offset")
			}
		}
	}
	for _, offset := range offsets {
		if offset != nil && (*offset < -3650 || *offset > 3650) {
			return v, invalid("Смещение должно быть от -3650 до 3650 календарных дней")
		}
	}
	for _, t := range v.Tasks {
		if t.ParentKey != nil {
			i, ok := keys[*t.ParentKey]
			if !ok || *t.ParentKey == t.Key || v.Tasks[i].ParentKey != nil {
				return v, invalid("Допустимы только корневая задача и подзадача; циклы запрещены")
			}
		}
	}
	if v.Tasks == nil {
		v.Tasks = []ProjectTemplateTask{}
	}
	return v, nil
}
func offsetDate(start string, offset *int) (*string, error) {
	if offset == nil {
		return nil, nil
	}
	d, e := parseDate(Date(start))
	if e != nil {
		return nil, e
	}
	d = d.AddDate(0, 0, *offset)
	if d.Year() < 1 || d.Year() > 9999 {
		return nil, invalid("Относительная дата выходит за границы 0001–9999")
	}
	value := d.Format("2006-01-02")
	return &value, nil
}
func composeProject(v ProjectTemplateSnapshot, date string) (ProjectComposition, error) {
	p := ProjectComposition{Appearance: v.Appearance, Title: v.Title, Description: v.Description, StartDate: date, Tasks: []ComposedTask{}}
	if e := managementDate(date); e != nil {
		return p, e
	}
	var e error
	p.DueDate, e = offsetDate(date, v.DueOffset)
	if e != nil {
		return p, e
	}
	for _, t := range v.Tasks {
		x := ComposedTask{Appearance: t.Appearance, Key: t.Key, ParentKey: t.ParentKey, Title: t.Title, Description: t.Description, Priority: t.Priority, ScheduledTime: t.ScheduledTime}
		x.PlannedDate, e = offsetDate(date, t.PlannedOffset)
		if e != nil {
			return p, e
		}
		x.DueDate, e = offsetDate(date, t.DueOffset)
		if e != nil {
			return p, e
		}
		p.Tasks = append(p.Tasks, x)
	}
	return p, nil
}
func (s *Service) instantiateProjectTx(tx *sql.Tx, snapshot ProjectTemplateSnapshot, version, date string, occurrence *string, op, now, actor string) (ProjectDTO, error) {
	c, e := composeProject(snapshot, date)
	if e != nil {
		return ProjectDTO{}, e
	}
	id, e := operation()
	if e != nil {
		return ProjectDTO{}, e
	}
	p := ProjectDTO{Appearance: c.Appearance, RecordMeta: RecordMeta{ID: id, Revision: 1, CreatedAt: now, UpdatedAt: now}, Title: c.Title, Description: c.Description, Status: "planned", StartDate: &date, DueDate: c.DueDate, OccurrenceID: occurrence, TemplateVersionID: &version}
	if e = insertProject(tx, p); e != nil {
		return p, e
	}
	if e = eventAs(tx, "project", id, "created_from_template", actor, op, now, nil, p); e != nil {
		return p, e
	}
	keys := map[string]string{}
	for _, t := range c.Tasks {
		id, e := operation()
		if e != nil {
			return p, e
		}
		keys[t.Key] = id
	}
	for _, children := range []bool{false, true} {
		for _, t := range c.Tasks {
			if (t.ParentKey != nil) != children {
				continue
			}
			var parent *string
			if t.ParentKey != nil {
				x := keys[*t.ParentKey]
				parent = &x
			}
			task := TaskDTO{Appearance: t.Appearance, RecordMeta: RecordMeta{ID: keys[t.Key], Revision: 1, CreatedAt: now, UpdatedAt: now}, ProjectID: &p.ID, ParentTaskID: parent, Title: t.Title, Description: t.Description, Priority: t.Priority, Status: "planned", PlannedDate: t.PlannedDate, DueDate: t.DueDate, ScheduledTime: t.ScheduledTime}
			if e = insertTask(tx, task); e != nil {
				return p, e
			}
			if e = eventAs(tx, "task", task.ID, "created_from_template", actor, op, now, nil, task); e != nil {
				return p, e
			}
		}
	}
	for i, t := range c.Tasks {
		if _, e = tx.Exec(`UPDATE tasks SET sort_order=? WHERE id=?`, i, keys[t.Key]); e != nil {
			return p, e
		}
	}
	if e = projectBaseline(tx, p.ID, keys); e != nil {
		return p, e
	}
	return projectWithTree(tx, p.ID)
}
func (s *Service) Template(ctx context.Context, id, version string) (ProjectTemplateDTO, error) {
	var v ProjectTemplateDTO
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error { var e error; v, e = getTemplate(tx, id, version); return e })
	return v, e
}
func (s *Service) Templates(ctx context.Context, archived bool, limit, offset int) ([]ProjectTemplateDTO, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, invalid("Некорректная пагинация")
	}
	items := []ProjectTemplateDTO{}
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		rows, e := tx.Query("SELECT id FROM project_templates WHERE template_kind='project' AND internal=0 AND archived=? ORDER BY created_at,id LIMIT ? OFFSET ?", archived, limit, offset)
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
			v, e := getTemplate(tx, id, "")
			if e != nil {
				return e
			}
			items = append(items, v)
		}
		return nil
	})
	return items, e
}
func (s *Service) TemplateCommand(ctx context.Context, id, action string, in TemplateInput) (CommandResult, error) {
	var result CommandResult
	now := stamp(s.now())
	op, e := operation()
	if e != nil {
		return result, e
	}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		result, e = commandOnce(tx, in.RequestID, "template/"+id, action, now, in, func() (CommandResult, error) {
			if in.Rule != nil || in.Count != 0 {
				return CommandResult{}, invalid("rule/count допустимы только для предпросмотра")
			}
			if action != "instantiate" && (in.StartDate != "" || in.TemplateVersionID != "") {
				return CommandResult{}, invalid("Дата/версия допустимы только при создании проекта")
			}
			var before ProjectTemplateDTO
			if action != "create" {
				var e error
				before, e = getTemplate(tx, id, "")
				if e != nil {
					return CommandResult{}, e
				}
				if e = checkRevision(in.ExpectedRevision, before.RecordMeta); e != nil {
					return CommandResult{}, e
				}
				if before.Archived {
					return CommandResult{}, invalid("Шаблон архивирован")
				}
			} else if in.ExpectedRevision != 0 {
				return CommandResult{}, invalid("expected_revision не используется при создании")
			}
			switch action {
			case "create", "versions":
				if in.Snapshot == nil {
					return CommandResult{}, invalid("Нужен snapshot")
				}
				snapshot, e := normalizeTemplate(*in.Snapshot)
				if e != nil {
					return CommandResult{}, e
				}
				if action == "create" {
					id, e = operation()
					if e != nil {
						return CommandResult{}, e
					}
					_, e = tx.Exec("INSERT INTO project_templates(id,title,created_at,updated_at) VALUES(?,?,?,?)", id, snapshot.Title, now, now)
				} else {
					_, e = tx.Exec("UPDATE project_templates SET title=?,revision=revision+1,updated_at=? WHERE id=?", snapshot.Title, now, id)
				}
				if e != nil {
					return CommandResult{}, e
				}
				if _, e = insertTemplateVersion(tx, id, snapshot, now); e != nil {
					return CommandResult{}, e
				}
			case "archive":
				if in.Snapshot != nil {
					return CommandResult{}, invalid("Архивирование не изменяет snapshot")
				}
				if _, e := tx.Exec("UPDATE project_templates SET archived=1,revision=revision+1,updated_at=? WHERE id=?", now, id); e != nil {
					return CommandResult{}, e
				}
			case "instantiate":
				if in.Snapshot != nil {
					return CommandResult{}, invalid("Используйте сохранённую версию шаблона")
				}
				v, e := getTemplate(tx, id, in.TemplateVersionID)
				if e != nil {
					return CommandResult{}, e
				}
				p, e := s.instantiateProjectTx(tx, v.Snapshot, v.VersionID, in.StartDate, nil, op, now, "owner")
				if e != nil {
					return CommandResult{}, e
				}
				if e = event(tx, "template", id, "instantiated", op, now, nil, map[string]any{"project_id": p.ID, "version_id": v.VersionID}); e != nil {
					return CommandResult{}, e
				}
				return CommandResult{Project: &p}, nil
			default:
				return CommandResult{}, invalid("Неизвестная команда шаблона")
			}
			v, e := getTemplate(tx, id, "")
			if e != nil {
				return CommandResult{}, e
			}
			if e = event(tx, "template", id, action, op, now, before, v); e != nil {
				return CommandResult{}, e
			}
			return CommandResult{Template: &v}, nil
		})
		return e
	})
	return result, e
}
func (s *Service) TemplatePreview(ctx context.Context, id string, in TemplateInput) ([]TemplatePreviewCycle, error) {
	result := []TemplatePreviewCycle{}
	if in.Snapshot != nil || in.RequestID != "" {
		return nil, invalid("Предпросмотр использует сохранённую версию")
	}
	if e := managementDate(in.StartDate); e != nil {
		return nil, e
	}
	count := in.Count
	if count == 0 {
		count = 5
	}
	if count < 1 || count > 20 {
		return nil, invalid("count: 1–20")
	}
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		v, e := getTemplate(tx, id, in.TemplateVersionID)
		if e != nil {
			return e
		}
		if in.ExpectedRevision != 0 {
			if e = checkRevision(in.ExpectedRevision, v.RecordMeta); e != nil {
				return e
			}
		}
		rule := recurrence.Rule{}
		if in.Rule != nil {
			rule, e = recurrence.NormalizeRule(*in.Rule)
			if e != nil {
				return recurrenceAPIError(e)
			}
			if rule.Mode != recurrence.Schedule {
				return invalid("Проекты поддерживают только schedule")
			}
		} else {
			c, e := composeProject(v.Snapshot, in.StartDate)
			if e != nil {
				return e
			}
			result = append(result, TemplatePreviewCycle{Project: c})
			return nil
		}
		series := SeriesDTO{Rule: StoredTaskRule{Rule: rule, FirstDate: recurrence.Date(in.StartDate), Anchor: recurrence.Anchor{Date: recurrence.Date(in.StartDate), Kind: recurrence.ScheduleAnchor}}}
		calendar := &recurrenceCalendarSnapshot{tx: tx, years: map[int]productionYearSnapshot{}}
		for i := 0; i < count; i++ {
			c, _, e := calculateStoredCycle(ctx, tx, series, int64(i), calendar)
			if e != nil {
				return e
			}
			p, e := composeProject(v.Snapshot, string(c.AdjustedDate))
			if e != nil {
				return e
			}
			result = append(result, TemplatePreviewCycle{Candidate: &c, Project: p})
		}
		return nil
	})
	return result, e
}
