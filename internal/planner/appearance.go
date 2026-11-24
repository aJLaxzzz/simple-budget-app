package planner

import (
	"database/sql"
	"regexp"
)

// Appearance contains only owned overrides. Nil means live inheritance, never a copy.
type Appearance struct {
	ColorID *string `json:"color_id,omitempty"`
	IconID  *string `json:"icon_id,omitempty"`
}
type EffectiveAppearance struct {
	EffectiveColorID string `json:"effective_color_id,omitempty"`
	EffectiveIconID  string `json:"effective_icon_id,omitempty"`
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func validateAppearance(tx *sql.Tx, a Appearance) error {
	if a.ColorID != nil {
		switch *a.ColorID {
		case "red", "burgundy", "pink", "magenta", "amber", "emerald", "mint", "aqua", "sky", "navy", "violet", "brown", "sand", "silver", "slate", "blue", "cyan", "teal", "green", "lime", "yellow", "orange", "coral", "rose", "purple", "indigo":
		default:
			if !hexColor.MatchString(*a.ColorID) {
				return invalid("Цвет: выберите оттенок или HEX в формате #RRGGBB")
			}
		}
	}
	if a.IconID != nil {
		var n int
		if e := tx.QueryRow("SELECT count(*) FROM planner_icons WHERE id=?", *a.IconID).Scan(&n); e != nil {
			return e
		}
		// Retired images remain available to immutable snapshots and their instances.
		if n != 1 {
			return invalid("Иконка не найдена в каталоге")
		}
	}
	return nil
}
