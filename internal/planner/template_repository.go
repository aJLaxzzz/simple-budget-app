package planner

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
)

func getTemplate(tx *sql.Tx, id, version string) (ProjectTemplateDTO, error) {
	v, e := getTemplateKind(tx, id, version, "project")
	return v, e
}
func getTemplateKind(tx *sql.Tx, id, version, kind string) (ProjectTemplateDTO, error) {
	var v ProjectTemplateDTO
	var raw string
	e := tx.QueryRow(`SELECT t.id,t.revision,t.created_at,t.updated_at,t.archived,t.template_kind,v.id,v.version,v.snapshot_json FROM project_templates t JOIN template_versions v ON v.template_id=t.id WHERE t.id=? AND (?='' OR t.template_kind=?) AND (?='' OR v.id=?) ORDER BY v.version DESC LIMIT 1`, id, kind, kind, version, version).Scan(&v.ID, &v.Revision, &v.CreatedAt, &v.UpdatedAt, &v.Archived, &v.Kind, &v.VersionID, &v.Version, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return v, notFound("Шаблон/версия", id)
	}
	if e != nil {
		return v, e
	}
	e = json.Unmarshal([]byte(raw), &v.Snapshot)
	return v, e
}
func templateByVersion(tx *sql.Tx, version string) (ProjectTemplateDTO, error) {
	var id string
	e := tx.QueryRow("SELECT template_id FROM template_versions WHERE id=?", version).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return ProjectTemplateDTO{}, notFound("Версия шаблона", version)
	}
	if e != nil {
		return ProjectTemplateDTO{}, e
	}
	return getTemplate(tx, id, version)
}
func insertTemplateVersion(tx *sql.Tx, id string, snapshot ProjectTemplateSnapshot, now string) (string, error) {
	if e := validateAppearance(tx, snapshot.Appearance); e != nil {
		return "", e
	}
	for _, t := range snapshot.Tasks {
		if e := validateAppearance(tx, t.Appearance); e != nil {
			return "", e
		}
	}
	version, e := operation()
	if e != nil {
		return "", e
	}
	raw, e := json.Marshal(snapshot)
	if e != nil {
		return "", e
	}
	_, e = tx.Exec(`INSERT INTO template_versions(id,template_id,version,snapshot_json,created_at) SELECT ?,?,COALESCE(max(version),0)+1,?,? FROM template_versions WHERE template_id=?`, version, id, string(raw), now, id)
	return version, e
}
func projectWithTree(tx *sql.Tx, id string) (ProjectDTO, error) {
	p, e := getProject(tx, id)
	if e == nil {
		e = decorateProject(tx, &p, true)
	}
	return p, e
}
func projectSessions(tx *sql.Tx, id string) ([]SessionDTO, error) {
	return listSessions(tx, "WHERE task_id IN(SELECT id FROM tasks WHERE project_id=?)", id)
}
func projectBaseline(tx *sql.Tx, id string, keys map[string]string) error {
	p, e := projectWithTree(tx, id)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return e
	}
	k, e := json.Marshal(keys)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO project_instances(project_id,baseline_json,task_keys_json) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET baseline_json=excluded.baseline_json,task_keys_json=excluded.task_keys_json`, id, string(raw), string(k))
	return e
}
func projectKeys(tx *sql.Tx, id string) (map[string]string, error) {
	var raw string
	e := tx.QueryRow("SELECT task_keys_json FROM project_instances WHERE project_id=?", id).Scan(&raw)
	keys := map[string]string{}
	if e == nil {
		e = json.Unmarshal([]byte(raw), &keys)
	}
	return keys, e
}
func pristineProject(tx *sql.Tx, p ProjectDTO) (bool, error) {
	var raw string
	e := tx.QueryRow("SELECT baseline_json FROM project_instances WHERE project_id=?", p.ID).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	current, e := json.Marshal(p)
	if e != nil {
		return false, e
	}
	var before, after any
	if e = json.Unmarshal([]byte(raw), &before); e != nil {
		return false, e
	}
	if e = json.Unmarshal(current, &after); e != nil {
		return false, e
	}
	var strip func(any)
	strip = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			delete(x, "effective_color_id")
			delete(x, "effective_icon_id")
			delete(x, "saved_template_id")
			delete(x, "source_template_id")
			delete(x, "repeat_series_id")
			for _, key := range []string{"managed_repeat", "project_mode", "current_project_id", "needs_decision", "preliminary", "project_pending", "routines"} {
				delete(x, key)
			}
			if x["sort_order"] == float64(0) {
				delete(x, "sort_order")
			}
			for _, v := range x {
				strip(v)
			}
		case []any:
			for _, v := range x {
				strip(v)
			}
		}
	}
	strip(before)
	strip(after)
	return reflect.DeepEqual(before, after), nil
}
