package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
)

func projectStarted(p *ProjectDTO, sessions []SessionDTO) bool {
	if p == nil {
		return false
	}
	if p.Status == "in_progress" || p.Status == "completed" {
		return true
	}
	for _, t := range p.Tasks {
		if t.Status == "in_progress" || t.Status == "completed" {
			return true
		}
	}
	for _, x := range sessions {
		if x.Status == "completed" || x.Status == "in_progress" {
			return true
		}
	}
	return false
}
func (s *Service) previewProjectSeriesTx(ctx context.Context, tx *sql.Tx, v SeriesDTO, in SeriesCommand) (SeriesChangePreview, error) {
	p := SeriesChangePreview{SeriesID: v.ID, Revision: v.Revision, Action: in.Action, CurrentRule: v.Rule, CurrentTemplate: v.ProjectTemplate, Rows: []SeriesChangeRow{}, Conflicts: []string{}, CalendarErrors: []*APIError{}}
	// Reuse the task-series command validation after removing project-only fields.
	cleanValidation := in
	cleanValidation.TemplateVersionID = ""
	cleanValidation.TasksDateDecision = ""
	cleanValidation.RemovedTasksDecision = ""
	if in.Action == "change_template" {
		cleanValidation.Action = "archive"
		cleanValidation.FromCycle = nil
	}
	if e := validateSeriesCommand(v, cleanValidation); e != nil {
		return p, e
	}
	if in.Action == "reanchor" {
		return p, invalid("Проекты повторяются только от расписания")
	}
	if in.TasksDateDecision != "" && in.Action != "change_rule" {
		return p, invalid("tasks_date_decision допустимо только для смены расписания")
	}
	if in.Action == "change_rule" && in.TasksDateDecision != "keep" && in.TasksDateDecision != "shift" {
		return p, invalid("Выберите tasks_date_decision: keep или shift")
	}
	if in.Action != "change_template" && (in.TemplateVersionID != "" || in.RemovedTasksDecision != "") {
		return p, invalid("Поля версии шаблона допустимы только для change_template")
	}
	proposed := v
	if in.Action == "change_rule" {
		rule, _ := recurrence.NormalizeRule(*in.Rule)
		proposed.Rule = StoredTaskRule{Rule: rule, FirstDate: recurrence.Date(in.FirstDate), FirstCycle: *in.FromCycle, Anchor: recurrence.Anchor{Date: recurrence.Date(in.FirstDate), CycleIndex: *in.FromCycle, Kind: recurrence.ScheduleAnchor}}
		p.ProposedRule = &proposed.Rule
	}
	if in.Action == "change_template" {
		if in.Rule != nil || in.FirstDate != "" || in.FromCycle == nil || *in.FromCycle < 0 || *in.FromCycle > v.NextCycle || in.TemplateVersionID == "" {
			return p, invalid("Укажите template_version_id и from_cycle без нового правила")
		}
		if in.RemovedTasksDecision != "" && in.RemovedTasksDecision != "cancel" {
			return p, invalid("removed_tasks_decision: cancel")
		}
		next, e := templateByVersion(tx, in.TemplateVersionID)
		if e != nil {
			return p, e
		}
		old, e := templateByVersion(tx, *v.TemplateVersionID)
		if e != nil {
			return p, e
		}
		if old.ID != next.ID || next.Archived {
			return p, invalid("Нужна неархивная версия того же шаблона")
		}
		if next.VersionID == *v.TemplateVersionID {
			return p, invalid("Эта версия шаблона уже используется")
		}
		proposed.ProjectTemplate = &next.Snapshot
		proposed.TemplateVersionID = &next.VersionID
		proposed.Rule.FirstCycle = *in.FromCycle
		p.ProposedRule = &proposed.Rule
		p.ProposedTemplate = proposed.ProjectTemplate
	}
	var e error
	p.Pauses, e = loadPauses(tx, v.ID)
	if e != nil {
		return p, e
	}
	if in.Action == "pause" {
		for _, pause := range p.Pauses {
			start := in.StartDate
			if pause.StartDate > start {
				start = pause.StartDate
			}
			end := in.EndDate
			if pause.EndDate != nil && *pause.EndDate < end {
				end = *pause.EndDate
			}
			if start <= end && (pause.ResumedOn == nil || start < *pause.ResumedOn) {
				return p, conflict("pause_overlap", "Паузы пересекаются", pause)
			}
		}
	}
	if in.Action == "resume" {
		found := false
		for _, pause := range p.Pauses {
			if pause.ID == in.PauseID {
				found = true
				if pause.ResumedOn != nil {
					return p, invalid("Пауза уже возобновлена")
				}
			}
		}
		if !found {
			return p, notFound("Пауза", in.PauseID)
		}
	}
	where := "o.series_id=?"
	args := []any{v.ID}
	switch in.Action {
	case "change_rule", "change_template":
		where += " AND o.cycle_index>=? AND (COALESCE(p.start_date,o.current_adjusted_date)>=? OR p.status='in_progress')"
		args = append(args, *in.FromCycle, s.today())
	case "pause":
		where += " AND (o.current_adjusted_date BETWEEN ? AND ? OR ((o.manual_override=1 OR o.manual_date IS NOT NULL) AND p.start_date BETWEEN ? AND ?))"
		args = append(args, in.StartDate, in.EndDate, in.StartDate, in.EndDate)
	case "resume":
		where += " AND o.current_adjusted_date>=? AND EXISTS(SELECT 1 FROM occurrence_incidents i WHERE i.occurrence_id=o.id AND i.pause_id=? AND i.disposition='needs_decision')"
		args = append(args, s.today(), in.PauseID)
	default:
		where += " AND o.cycle_index>=?"
		args = append(args, v.NextCycle-1)
	}
	rows, e := tx.Query("SELECT o.id FROM occurrence_current o LEFT JOIN projects p ON p.occurrence_id=o.id WHERE "+where+" ORDER BY o.cycle_index LIMIT 101", args...)
	if e != nil {
		return p, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return p, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return p, e
	}
	if len(ids) > 100 {
		return p, invalid("Более 100 экземпляров проектов; сузьте область изменения")
	}
	var total int
	if e = tx.QueryRow("SELECT count(*) FROM occurrences WHERE series_id=?", v.ID).Scan(&total); e != nil {
		return p, e
	}
	p.UnchangedOutsideScope = total - len(ids)
	calendar := &recurrenceCalendarSnapshot{tx: tx, years: map[int]productionYearSnapshot{}}
	treeSize := 0
	for _, id := range ids {
		o, e := getOccurrence(tx, id)
		if e != nil {
			return p, e
		}
		if o.Project != nil {
			treeSize += len(o.Project.Tasks)
			if treeSize > 5000 {
				return p, invalid("Более 5000 задач в затрагиваемых проектах; сузьте диапазон")
			}
		}
		date := o.AdjustedDate
		if o.Project != nil && o.Project.StartDate != nil {
			date = *o.Project.StartDate
		}
		row := SeriesChangeRow{OccurrenceID: id, CycleIndex: o.CycleIndex, OccurrenceRevision: o.Revision, BeforeDate: &date, AfterDate: &date, BaseDate: o.BaseDate, AdjustedDate: o.AdjustedDate, Manual: o.ManualOverride || o.ManualDate != nil, Project: o.Project, Disposition: "unchanged", Sessions: []SessionDTO{}}
		pristine := true
		if o.Project != nil {
			row.Sessions, e = projectSessions(tx, o.Project.ID)
			if e != nil {
				return p, e
			}
			pristine, e = pristineProject(tx, *o.Project)
			if e != nil {
				return p, e
			}
		}
		started := projectStarted(o.Project, row.Sessions)
		closed := !active(o.ProjectStatus)
		if in.Action == "resume" {
			if o.ProjectStatus == "skipped" && !row.Manual {
				row.Disposition = "restore_after_resume"
			}
			p.Rows = append(p.Rows, row)
			continue
		}
		if in.Action == "pause" || in.Action == "change_rule" || in.Action == "change_template" {
			switch {
			case closed:
				row.Disposition = "protected_closed"
			case started:
				row.Disposition = "protected_started"
			case row.Manual:
				row.Disposition = "manual_exception"
			case o.Project != nil && o.Project.ArchivedAt != nil:
				row.Disposition = "protected_archived"
			case !pristine:
				row.Disposition = "protected_user_changes"
			case len(row.Sessions) > 0:
				row.Disposition = "protected_sessions"
			default:
				if in.Action == "pause" {
					row.Disposition = "skip_pause"
				} else {
					c := recurrence.Candidate{CycleIndex: o.CycleIndex, BaseDate: recurrence.Date(o.BaseDate), AdjustedDate: recurrence.Date(o.AdjustedDate)}
					if in.Action == "change_rule" {
						var err error
						c, row.Calendars, err = calculateStoredCycle(ctx, tx, proposed, o.CycleIndex, calendar)
						if err != nil {
							var api *APIError
							if !errors.As(recurrenceAPIError(err), &api) {
								return p, err
							}
							p.CalendarErrors = append(p.CalendarErrors, api)
							row.Disposition = "calculation_error"
							p.Rows = append(p.Rows, row)
							continue
						}
					}
					target := string(c.AdjustedDate)
					row.AfterDate = &target
					row.Candidate = &c
					row.Disposition = "change_project"
					compositionTemplate := proposed.ProjectTemplate
					row.TemplateVersionID = proposed.TemplateVersionID
					// A rule edit does not implicitly apply a different tree version.
					if in.Action == "change_rule" && o.Project != nil && o.Project.TemplateVersionID != nil {
						stored, e := templateByVersion(tx, *o.Project.TemplateVersionID)
						if e != nil {
							return p, e
						}
						compositionTemplate = &stored.Snapshot
						row.TemplateVersionID = o.Project.TemplateVersionID
					}
					composition, e := composeProject(*compositionTemplate, target)
					if e != nil {
						return p, e
					}
					if in.Action == "change_rule" && in.TasksDateDecision == "keep" && o.Project != nil {
						keys, e := projectKeys(tx, o.Project.ID)
						if e != nil {
							return p, e
						}
						byID := map[string]TaskDTO{}
						for _, t := range o.Project.Tasks {
							byID[t.ID] = t
						}
						for i := range composition.Tasks {
							t := byID[keys[composition.Tasks[i].Key]]
							composition.Tasks[i].PlannedDate = t.PlannedDate
							composition.Tasks[i].DueDate = t.DueDate
						}
					}
					row.ProposedProject = &composition
					if in.Action == "change_template" && o.Project != nil {
						if e = checkTemplateTreeChange(tx, *o.Project, composition, in.RemovedTasksDecision); e != nil {
							row.Disposition = "conflict_structure"
							row.Reason = e.Error()
							p.Conflicts = append(p.Conflicts, o.ID+": "+e.Error())
						}
					}
					if row.Disposition != "conflict_structure" && pauseOn(p.Pauses, target) != nil {
						row.Disposition = "change_and_skip"
						row.PauseConflict = true
						row.Reason = "Новая дата проекта попадает в паузу"
					}
				}
			}
			if in.Action == "pause" && row.Disposition != "skip_pause" && row.Disposition != "protected_closed" {
				row.PauseConflict = true
				row.Reason = "Существующее состояние проекта сохранено"
			}
		}
		if row.PauseConflict {
			p.Conflicts = append(p.Conflicts, id+": "+row.Disposition)
		}
		p.Rows = append(p.Rows, row)
	}
	// Future dates and composition use the same calculator as materialization.
	if in.Action == "change_rule" || in.Action == "change_template" || in.Action == "pause" {
		for i := int64(0); i < 10; i++ {
			index := v.NextCycle + i
			base, e := recurrence.BaseDate(proposed.Rule.Rule, proposed.Rule.Anchor, index)
			if e != nil {
				return p, recurrenceAPIError(e)
			}
			end, _ := parseDate(Date(v.HorizonDate))
			if proposed.Rule.Rule.Adjustment == recurrence.Previous || proposed.Rule.Rule.Adjustment == recurrence.Nearest {
				end = end.AddDate(0, 0, recurrence.MaxAdjustmentDays)
			}
			if string(base) > end.Format("2006-01-02") {
				break
			}
			c, cal, e := calculateStoredCycle(ctx, tx, proposed, index, calendar)
			if e != nil {
				var api *APIError
				if !errors.As(recurrenceAPIError(e), &api) {
					return p, e
				}
				p.CalendarErrors = append(p.CalendarErrors, api)
				break
			}
			date := string(c.AdjustedDate)
			if date > v.HorizonDate {
				break
			}
			composition, e := composeProject(*proposed.ProjectTemplate, date)
			if e != nil {
				return p, e
			}
			disposition := "not_materialized"
			if pauseOn(p.Pauses, date) != nil || in.Action == "pause" && date >= in.StartDate && date <= in.EndDate {
				disposition = "future_pause_skip"
			}
			p.Rows = append(p.Rows, SeriesChangeRow{CycleIndex: index, BaseDate: string(c.BaseDate), AdjustedDate: date, AfterDate: &date, Candidate: &c, Calendars: cal, ProposedProject: &composition, Disposition: disposition, Sessions: []SessionDTO{}})
		}
	}
	clean := in
	clean.RequestID = ""
	clean.PreviewToken = ""
	p.PreviewToken, e = digest(struct {
		Input   SeriesCommand
		Preview SeriesChangePreview
		Today   string
	}{clean, p, s.today()})
	return p, e
}
func checkTemplateTreeChange(tx *sql.Tx, p ProjectDTO, c ProjectComposition, removed string) error {
	keys, e := projectKeys(tx, p.ID)
	if e != nil {
		return e
	}
	wanted := map[string]bool{}
	for _, t := range c.Tasks {
		wanted[t.Key] = true
	}
	tasks := map[string]TaskDTO{}
	for _, t := range p.Tasks {
		tasks[t.ID] = t
	}
	for key, id := range keys {
		if !wanted[key] {
			if removed != "cancel" {
				return conflict("template_removal_confirmation", "Удаление из состава требует removed_tasks_decision=cancel; задачи сохранятся отменёнными", tasks[id])
			}
		}
	}
	for _, t := range c.Tasks {
		if id, ok := keys[t.Key]; ok {
			old := tasks[id]
			var parent *string
			if t.ParentKey != nil {
				id, ok := keys[*t.ParentKey]
				if !ok {
					return conflict("template_reparent_conflict", "Перенос существующей задачи к новому родителю требует ручного решения", old)
				}
				parent = &id
			}
			if !reflect.DeepEqual(old.ParentTaskID, parent) {
				return conflict("template_reparent_conflict", "Изменение родителя существующей задачи требует ручного решения", old)
			}
		}
	}
	return nil
}
func (s *Service) applyProjectComposition(ctx context.Context, tx *sql.Tx, p ProjectDTO, c ProjectComposition, version, removed, op, now string) error {
	if e := checkTemplateTreeChange(tx, p, c, removed); e != nil {
		return e
	}
	keys, e := projectKeys(tx, p.ID)
	if e != nil {
		return e
	}
	oldKeys := map[string]string{}
	for k, v := range keys {
		oldKeys[k] = v
	}
	wanted := map[string]bool{}
	for _, t := range c.Tasks {
		wanted[t.Key] = true
		if _, ok := keys[t.Key]; !ok {
			id, e := operation()
			if e != nil {
				return e
			}
			keys[t.Key] = id
		}
	}
	for key, id := range oldKeys {
		if wanted[key] {
			continue
		}
		t, e := getTask(tx, id)
		if e != nil {
			return e
		}
		if active(t.Status) {
			var changed TaskDTO
			if e = s.changeTaskTx(ctx, tx, id, "cancel", TaskInput{Mutation: Mutation{ExpectedRevision: t.Revision}}, op, now, s.now(), &changed, taskChangeOptions{}); e != nil {
				return e
			}
		}
		delete(keys, key)
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
			id := keys[t.Key]
			if _, exists := oldKeys[t.Key]; exists {
				old, e := getTask(tx, id)
				if e != nil {
					return e
				}
				next := old
				next.Appearance = t.Appearance
				next.Title = t.Title
				next.Description = t.Description
				next.Priority = t.Priority
				next.PlannedDate = t.PlannedDate
				next.DueDate = t.DueDate
				next.ScheduledTime = t.ScheduledTime
				next.UpdatedAt = now
				if e = saveTask(tx, next); e != nil {
					return e
				}
				next.Revision++
				if e = event(tx, "task", id, "template_version_applied", op, now, old, next); e != nil {
					return e
				}
			} else {
				next := TaskDTO{Appearance: t.Appearance, RecordMeta: RecordMeta{ID: id, Revision: 1, CreatedAt: now, UpdatedAt: now}, ProjectID: &p.ID, ParentTaskID: parent, Title: t.Title, Description: t.Description, Priority: t.Priority, Status: "planned", PlannedDate: t.PlannedDate, DueDate: t.DueDate, ScheduledTime: t.ScheduledTime}
				if e = insertTask(tx, next); e != nil {
					return e
				}
				if e = event(tx, "task", id, "created_from_template", op, now, nil, next); e != nil {
					return e
				}
			}
		}
	}
	old := p
	p.Appearance = c.Appearance
	p.Title = c.Title
	p.Description = c.Description
	p.StartDate = &c.StartDate
	p.DueDate = c.DueDate
	p.TemplateVersionID = &version
	p.UpdatedAt = now
	if e = saveProject(tx, p); e != nil {
		return e
	}
	p.Revision++
	if _, e = tx.Exec("UPDATE projects SET template_version_id=? WHERE id=?", version, p.ID); e != nil {
		return e
	}
	if e = event(tx, "project", p.ID, "series_version_applied", op, now, old, p); e != nil {
		return e
	}
	return projectBaseline(tx, p.ID, keys)
}
func (s *Service) applyProjectSeriesTx(ctx context.Context, tx *sql.Tx, v SeriesDTO, in SeriesCommand, op, now string) (CommandResult, error) {
	p, e := s.previewProjectSeriesTx(ctx, tx, v, in)
	if e != nil {
		return CommandResult{}, e
	}
	if p.PreviewToken != in.PreviewToken || in.PreviewToken == "" {
		return CommandResult{}, conflict("preview_changed", "Просмотрите актуальный предпросмотр", p)
	}
	if len(p.CalendarErrors) > 0 {
		return CommandResult{}, p.CalendarErrors[0]
	}
	for _, row := range p.Rows {
		if row.Disposition == "conflict_structure" {
			return CommandResult{}, conflict("template_structure_conflict", row.Reason, p)
		}
	}
	before := v
	switch in.Action {
	case "change_rule", "change_template":
		selected := v
		if in.Action == "change_template" {
			selected.ProjectTemplate = p.ProposedTemplate
			selected.TemplateVersionID = &in.TemplateVersionID
		}
		version, e := insertVersion(tx, selected, *p.ProposedRule, now)
		if e != nil {
			return CommandResult{}, e
		}
		for _, row := range p.Rows {
			if row.OccurrenceID == "" || row.ProposedProject == nil || row.Candidate == nil {
				continue
			}
			if e = s.applyProjectComposition(ctx, tx, *row.Project, *row.ProposedProject, *row.TemplateVersionID, in.RemovedTasksDecision, op, now); e != nil {
				return CommandResult{}, e
			}
			raw, e := json.Marshal(map[string]any{"candidate": row.Candidate, "calendars": row.Calendars})
			if e != nil {
				return CommandResult{}, e
			}
			_, e = tx.Exec(`INSERT INTO occurrence_schedule(occurrence_id,series_id,rule_version_id,base_date,adjusted_date,calculation_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(occurrence_id) DO UPDATE SET rule_version_id=excluded.rule_version_id,base_date=excluded.base_date,adjusted_date=excluded.adjusted_date,calculation_json=excluded.calculation_json,revision=occurrence_schedule.revision+1,updated_at=excluded.updated_at`, row.OccurrenceID, v.ID, version, row.Candidate.BaseDate, row.Candidate.AdjustedDate, string(raw), now, now)
			if e != nil {
				return CommandResult{}, e
			}
			if _, e = tx.Exec("UPDATE occurrences SET revision=revision+1,updated_at=? WHERE id=?", now, row.OccurrenceID); e != nil {
				return CommandResult{}, e
			}
			if e = event(tx, "occurrence", row.OccurrenceID, "series_version_applied", op, now, row, map[string]any{"rule_version_id": version, "date": row.AfterDate}); e != nil {
				return CommandResult{}, e
			}
			if row.Disposition == "change_and_skip" {
				pause := pauseOn(p.Pauses, *row.AfterDate)
				if e = s.automaticProjectAction(ctx, tx, v.ID, row.OccurrenceID, "skip", pause.ID, op, now); e != nil {
					return CommandResult{}, e
				}
			}
		}
	case "pause":
		id, e := operation()
		if e != nil {
			return CommandResult{}, e
		}
		_, e = tx.Exec("INSERT INTO series_pauses(id,series_id,start_date,end_date,reason,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", id, v.ID, in.StartDate, in.EndDate, in.Reason, now, now)
		if e != nil {
			return CommandResult{}, e
		}
		for _, row := range p.Rows {
			if row.Disposition == "skip_pause" {
				if e = s.automaticProjectAction(ctx, tx, v.ID, row.OccurrenceID, "skip", id, op, now); e != nil {
					return CommandResult{}, e
				}
			}
		}
	case "resume":
		if _, e = tx.Exec("UPDATE series_pauses SET resumed_on=?,revision=revision+1,updated_at=? WHERE id=? AND series_id=?", s.today(), now, in.PauseID, v.ID); e != nil {
			return CommandResult{}, e
		}
		for _, row := range p.Rows {
			if row.Disposition == "restore_after_resume" {
				if e = s.automaticProjectAction(ctx, tx, v.ID, row.OccurrenceID, "restore", in.PauseID, op, now); e != nil {
					return CommandResult{}, e
				}
			}
		}
	case "archive":
	}
	v, e = getSeries(tx, v.ID)
	if e != nil {
		return CommandResult{}, e
	}
	if in.Action == "archive" {
		v.State = "archived"
	}
	if e = s.refreshPauseState(tx, &v); e != nil {
		return CommandResult{}, e
	}
	if e = saveSeriesRuntime(tx, v, now); e != nil {
		return CommandResult{}, e
	}
	v.Revision++
	if e = event(tx, "series", v.ID, in.Action, op, now, before, map[string]any{"command": in, "preview": p}); e != nil {
		return CommandResult{}, e
	}
	if _, _, e = s.materializeSeriesTx(ctx, tx, &v, op, now); e != nil {
		return CommandResult{}, e
	}
	v, e = getSeries(tx, v.ID)
	if e != nil {
		return CommandResult{}, e
	}
	if e = decorateSeries(tx, &v); e != nil {
		return CommandResult{}, e
	}
	return CommandResult{Series: &v}, nil
}
func (s *Service) automaticProjectAction(ctx context.Context, tx *sql.Tx, series, id, action, pause, op, now string) error {
	v, e := getSeries(tx, series)
	if e != nil {
		return e
	}
	o, e := getOccurrence(tx, id)
	if e != nil {
		return e
	}
	in := OccurrenceCommand{ExpectedRevision: o.Revision, ExpectedSeriesRevision: v.Revision, Reason: pause, RelatedRevisions: map[string]int64{}}
	if o.Project != nil {
		in.ExpectedProjectRevision = o.Project.Revision
		for _, t := range o.Project.Tasks {
			in.RelatedRevisions[t.ID] = t.Revision
		}
	}
	if action == "restore" {
		in.TasksDecision = "restore_closed"
	}
	_, e = s.projectOccurrenceCommandTx(ctx, tx, v, o, action, in, op, now, true)
	return e
}
