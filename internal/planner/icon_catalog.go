package planner

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
)

//go:embed icons/catalog.json
var iconCatalogJSON []byte

// Catalog metadata is shipped with the application, not supplied by clients.
var iconCatalog = func() map[string]IconDTO {
	var icons []IconDTO
	if err := json.Unmarshal(iconCatalogJSON, &icons); err != nil {
		panic(err)
	}
	result := make(map[string]IconDTO, len(icons))
	for _, icon := range icons {
		result[icon.ID] = icon
	}
	return result
}()

// Additive seed, also used after restore. Never rewrites an existing reference,
// image, revision, or user metadata. No schema change is necessary.
func seedBuiltinIcons(ctx context.Context, tx *sql.Tx) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO planner_icons(id,name,category,kind,mime,checksum,width,height,created_at,updated_at)
 VALUES(?,?,?,'builtin','image/svg+xml',?,24,24,'2026-11-21T00:00:00Z','2026-11-21T00:00:00Z') ON CONFLICT(id) DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, icon := range iconCatalog {
		if _, err = stmt.ExecContext(ctx, icon.ID, icon.Name, icon.Category, icon.Checksum); err != nil {
			return err
		}
	}
	return nil
}
