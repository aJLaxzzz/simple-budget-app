package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
)

type SeriesAppearanceInput struct {
	Appearance
	ExpectedRevision int64  `json:"expected_revision"`
	RequestID        string `json:"request_id,omitempty"`
	PreviewToken     string `json:"preview_token,omitempty"`
}
type AppearanceChange struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Revision   int64      `json:"revision"`
	Before     Appearance `json:"before"`
	After      Appearance `json:"after"`
	Protected  bool       `json:"protected"`
	Individual bool       `json:"individual"`
}
type AppearancePreview struct {
	SeriesID     string             `json:"series_id"`
	Revision     int64              `json:"revision"`
	Before       Appearance         `json:"before"`
	After        Appearance         `json:"after"`
	Rows         []AppearanceChange `json:"rows"`
	PreviewToken string             `json:"preview_token"`
}

func seriesAppearance(tx *sql.Tx, id string) (Appearance, bool, error) {
	var a Appearance
	var raw string
	e := tx.QueryRow("SELECT appearance_json FROM series_appearances WHERE series_id=? ORDER BY version DESC LIMIT 1", id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return a, false, nil
	}
	if e != nil {
		return a, false, e
	}
	e = json.Unmarshal([]byte(raw), &a)
	return a, true, e
}

// Compare each property with the source for the occurrence's own version. A local
// color must not prevent an inherited icon from following the repetition.
func inheritedAppearance(current, source, next Appearance) Appearance {
	result := current
	if reflect.DeepEqual(current.ColorID, source.ColorID) {
		result.ColorID = next.ColorID
	}
	if reflect.DeepEqual(current.IconID, source.IconID) {
		result.IconID = next.IconID
	}
	return result
}
func appearancePreview(tx *sql.Tx, id string, in SeriesAppearanceInput) (AppearancePreview, error) {
	p := AppearancePreview{SeriesID: id, After: in.Appearance, Rows: []AppearanceChange{}}
	s, e := getSeries(tx, id)
	if e != nil {
		return p, e
	}
	if e = checkRevision(in.ExpectedRevision, s.RecordMeta); e != nil {
		return p, e
	}
	if e = validateAppearance(tx, in.Appearance); e != nil {
		return p, e
	}
	p.Revision = s.Revision
	p.Before = s.Template.Appearance
	if s.ProjectTemplate != nil {
		p.Before = s.ProjectTemplate.Appearance
	}
	previous, hasPrevious, e := seriesAppearance(tx, id)
	if e != nil {
		return p, e
	}
	table := "tasks"
	if s.SourceType == "project" {
		table = "projects"
	}
	rows, e := tx.Query("SELECT t.id FROM "+table+" t JOIN occurrences o ON o.id=t.occurrence_id WHERE o.series_id=? ORDER BY o.cycle_index", id)
	if e != nil {
		return p, e
	}
	ids := []string{}
	for rows.Next() {
		var oid string
		if e = rows.Scan(&oid); e != nil {
			rows.Close()
			return p, e
		}
		ids = append(ids, oid)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return p, e
	}
	for _, oid := range ids {
		var a Appearance
		var m RecordMeta
		var title, status, occ string
		var archived bool
		if table == "tasks" {
			t, err := getTask(tx, oid)
			if err != nil {
				return p, err
			}
			a = t.Appearance
			m = t.RecordMeta
			title = t.Title
			status = t.Status
			occ = *t.OccurrenceID
			if t.ProjectID != nil {
				project, err := getProject(tx, *t.ProjectID)
				if err != nil {
					return p, err
				}
				archived = project.ArchivedAt != nil
			}
		} else {
			t, err := getProject(tx, oid)
			if err != nil {
				return p, err
			}
			a = t.Appearance
			m = t.RecordMeta
			title = t.Title
			status = t.Status
			occ = *t.OccurrenceID
			archived = t.ArchivedAt != nil
		}
		source := previous
		if !hasPrevious {
			var raw string
			if e = tx.QueryRow("SELECT v.source_snapshot_json FROM occurrences o JOIN series_versions v ON v.id=o.rule_version_id WHERE o.id=?", occ).Scan(&raw); e != nil {
				return p, e
			}
			if e = json.Unmarshal([]byte(raw), &source); e != nil {
				return p, e
			}
		}
		// Explicit local edits remain overrides even when their value happens to
		// equal the old source. Existing immutable history supplies provenance.
		column := "task_id"
		if table == "projects" {
			column = "project_id"
		}
		var ownColor, ownIcon bool
		query := "SELECT EXISTS(SELECT 1 FROM history_events WHERE " + column + "=? AND actor='owner' AND action NOT IN ('created','recurrence_linked','appearance_changed') AND rowid>(SELECT coalesce(max(rowid),0) FROM history_events WHERE " + column + "=? AND action IN ('created','recurrence_linked')) AND json_type(payload_json,'$.color_id') IS NOT NULL), EXISTS(SELECT 1 FROM history_events WHERE " + column + "=? AND actor='owner' AND action NOT IN ('created','recurrence_linked','appearance_changed') AND rowid>(SELECT coalesce(max(rowid),0) FROM history_events WHERE " + column + "=? AND action IN ('created','recurrence_linked')) AND json_type(payload_json,'$.icon_id') IS NOT NULL)"
		if e = tx.QueryRow(query, oid, oid, oid, oid).Scan(&ownColor, &ownIcon); e != nil {
			return p, e
		}
		next := inheritedAppearance(a, source, in.Appearance)
		if ownColor {
			next.ColorID = a.ColorID
		}
		if ownIcon {
			next.IconID = a.IconID
		}
		protected := !active(status) || archived
		if protected {
			next = a
		}
		p.Rows = append(p.Rows, AppearanceChange{ID: oid, Title: title, Revision: m.Revision, Before: a, After: next, Protected: protected, Individual: ownColor || ownIcon || !reflect.DeepEqual(a, source)})
	}
	p.PreviewToken, e = digest(p)
	return p, e
}
func (s *Service) PreviewSeriesAppearance(ctx context.Context, id string, in SeriesAppearanceInput) (AppearancePreview, error) {
	var p AppearancePreview
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error { var e error; p, e = appearancePreview(tx, id, in); return e })
	return p, e
}
func (s *Service) ApplySeriesAppearance(ctx context.Context, id string, in SeriesAppearanceInput) (CommandResult, error) {
	var result CommandResult
	now := stamp(s.now())
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		result, e = commandOnce(tx, in.RequestID, id, "appearance", now, in, func() (CommandResult, error) {
			p, e := appearancePreview(tx, id, in)
			if e != nil {
				return CommandResult{}, e
			}
			if in.PreviewToken == "" || in.PreviewToken != p.PreviewToken {
				return CommandResult{}, conflict("preview_changed", "Оформление или состав выполнений изменились. Повторите предпросмотр; черновик сохранён.", nil)
			}
			s0, e := getSeries(tx, id)
			if e != nil {
				return CommandResult{}, e
			}
			vid, e := operation()
			if e != nil {
				return CommandResult{}, e
			}
			raw, _ := json.Marshal(in.Appearance)
			if _, e = tx.Exec("INSERT INTO series_appearances(id,series_id,version,appearance_json,created_at) VALUES(?,?,(SELECT coalesce(max(version),0)+1 FROM series_appearances WHERE series_id=?),?,?)", vid, id, id, string(raw), now); e != nil {
				return CommandResult{}, e
			}
			for _, row := range p.Rows {
				if row.Protected || reflect.DeepEqual(row.Before, row.After) {
					continue
				}
				if s0.SourceType == "task" {
					t, e := getTask(tx, row.ID)
					if e != nil {
						return CommandResult{}, e
					}
					before := t
					t.Appearance = row.After
					t.UpdatedAt = now
					if e = saveTask(tx, t); e != nil {
						return CommandResult{}, e
					}
					t.Revision++
					if e = event(tx, "task", t.ID, "appearance_changed", in.RequestID, now, before, t); e != nil {
						return CommandResult{}, e
					}
				} else {
					t, e := getProject(tx, row.ID)
					if e != nil {
						return CommandResult{}, e
					}
					before := t
					t.Appearance = row.After
					t.UpdatedAt = now
					if e = saveProject(tx, t); e != nil {
						return CommandResult{}, e
					}
					t.Revision++
					if e = event(tx, "project", t.ID, "appearance_changed", in.RequestID, now, before, t); e != nil {
						return CommandResult{}, e
					}
				}
			}
			if _, e = tx.Exec("UPDATE series SET revision=revision+1,updated_at=? WHERE id=?", now, id); e != nil {
				return CommandResult{}, e
			}
			if e = event(tx, "series", id, "appearance_changed", in.RequestID, now, p.Before, p); e != nil {
				return CommandResult{}, e
			}
			fresh, e := getSeries(tx, id)
			return CommandResult{Series: &fresh}, e
		})
		return e
	})
	return result, e
}
func seriesAppearanceRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in SeriesAppearanceInput
	if e := strictPlannerInput(w, r, &in, "color_id icon_id expected_revision request_id preview_token"); e != nil {
		return 0, nil, e
	}
	if action == "appearance-preview" {
		p, e := s.PreviewSeriesAppearance(r.Context(), id, in)
		return 200, p, e
	}
	v, e := s.ApplySeriesAppearance(r.Context(), id, in)
	return 200, v, e
}

func effectiveSeriesAppearance(tx *sql.Tx, s *SeriesDTO) error {
	a := s.Template.Appearance
	inherited := EffectiveAppearance{EffectiveColorID: "slate", EffectiveIconID: "lucide:circle-check"}
	if s.ProjectTemplate != nil {
		a = s.ProjectTemplate.Appearance
		inherited.EffectiveIconID = "lucide:folder"
	} else if s.Template.ParentTaskID != nil {
		parent, e := getTask(tx, *s.Template.ParentTaskID)
		if e != nil {
			return e
		}
		inherited = parent.EffectiveAppearance
	} else if s.Template.ProjectID != nil {
		project, e := getProject(tx, *s.Template.ProjectID)
		if e != nil {
			return e
		}
		inherited = project.EffectiveAppearance
	}
	if a.ColorID != nil {
		inherited.EffectiveColorID = *a.ColorID
	}
	if a.IconID != nil {
		inherited.EffectiveIconID = *a.IconID
	}
	s.EffectiveAppearance = inherited
	return nil
}
