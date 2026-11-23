package planner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

//go:embed icons/*.svg
var builtinIcons embed.FS

type IconDTO struct {
	RecordMeta
	Synonyms  []string `json:"synonyms,omitempty"`
	Name      string   `json:"name"`
	Category  string   `json:"category"`
	Kind      string   `json:"kind"`
	Checksum  string   `json:"checksum"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	DeletedAt *string  `json:"deleted_at,omitempty"`
}

func scanIcon(row rowScanner) (IconDTO, error) {
	var v IconDTO
	e := row.Scan(&v.ID, &v.Revision, &v.CreatedAt, &v.UpdatedAt, &v.Name, &v.Category, &v.Kind, &v.Checksum, &v.Width, &v.Height, &v.DeletedAt)
	if entry, ok := iconCatalog[v.ID]; ok && v.Kind == "builtin" {
		v.Name, v.Category, v.Synonyms = entry.Name, entry.Category, entry.Synonyms
	}
	return v, e
}

const iconColumns = "id,revision,created_at,updated_at,name,category,kind,checksum,width,height,deleted_at"

func normalizeIcon(raw []byte) ([]byte, int, int, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return nil, 0, 0, invalid("Изображение должно быть не больше 1 MiB")
	}
	cfg, format, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil || (format != "png" && format != "webp") {
		return nil, 0, 0, invalid("Допустимы только корректные PNG и WebP")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 1024 || cfg.Height > 1024 {
		return nil, 0, 0, invalid("Размер изображения: от 1 до 1024 пикселей по каждой стороне")
	}
	img, _, e := image.Decode(bytes.NewReader(raw))
	if e != nil {
		return nil, 0, 0, invalid("Повреждённое изображение")
	}
	var b bytes.Buffer
	if e = png.Encode(&b, img); e != nil {
		return nil, 0, 0, e
	}
	if b.Len() > 1<<20 {
		return nil, 0, 0, invalid("Изображение после обработки больше 1 MiB")
	}
	return b.Bytes(), cfg.Width, cfg.Height, nil
}
func iconEvent(tx *sql.Tx, id, action, now string, changes any) error {
	op, e := operation()
	if e != nil {
		return e
	}
	raw, e := json.Marshal(changes)
	if e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO icon_events(id,icon_id,action,operation_id,changes_json,created_at) VALUES(?,?,?,?,?,?)", op, id, action, op, string(raw), now)
	return e
}
func (s *Service) iconsHTTP(w http.ResponseWriter, r *http.Request) {
	v, status, e := s.iconsRequest(w, r)
	if e != nil {
		WriteAPIError(w, e)
		return
	}
	if status != 0 {
		writeJSON(w, status, v)
	}
}
func (s *Service) iconsRequest(w http.ResponseWriter, r *http.Request) (any, int, error) {
	path := strings.TrimPrefix(r.URL.Path, "/api/planner/")
	parts := strings.Split(path, "/")
	if parts[0] != "icons" || len(parts) > 3 {
		return nil, 0, notFound("Маршрут", path)
	}
	id := ""
	if len(parts) > 1 {
		id = parts[1]
		if id == "" {
			return nil, 0, notFound("Иконка", id)
		}
	}
	if len(parts) == 3 {
		if parts[2] != "image" {
			return nil, 0, notFound("Маршрут", path)
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			return nil, 0, methodError(w, "GET, HEAD")
		}
		var data []byte
		var mime, kind string
		e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
			return tx.QueryRow("SELECT kind,mime,image FROM planner_icons WHERE id=?", id).Scan(&kind, &mime, &data)
		})
		if e == sql.ErrNoRows {
			return nil, 0, notFound("Иконка", id)
		}
		if e != nil {
			return nil, 0, e
		}
		if kind == "builtin" {
			data, e = builtinIcons.ReadFile("icons/" + strings.TrimPrefix(id, "lucide:") + ".svg")
			if e != nil {
				return nil, 0, e
			}
		}
		if r.URL.Query().Get("size") != "" {
			if r.URL.Query().Get("size") != "64" {
				return nil, 0, invalid("Допустимый размер миниатюры: 64")
			}
			if kind == "custom" {
				img, err := png.Decode(bytes.NewReader(data))
				if err != nil {
					return nil, 0, err
				}
				bounds := img.Bounds()
				width, height := bounds.Dx(), bounds.Dy()
				if width > 64 || height > 64 {
					if width >= height {
						height = max(1, height*64/width)
						width = 64
					} else {
						width = max(1, width*64/height)
						height = 64
					}
					thumb := image.NewRGBA(image.Rect(0, 0, width, height))
					draw.CatmullRom.Scale(thumb, thumb.Bounds(), img, bounds, draw.Src, nil)
					var b bytes.Buffer
					if err = png.Encode(&b, thumb); err != nil {
						return nil, 0, err
					}
					data = b.Bytes()
				}
			}
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		if r.Method == "GET" {
			_, e = w.Write(data)
		}
		return nil, 0, e
	}
	if id == "" && r.Method == "GET" {
		items := []IconDTO{}
		e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
			rows, e := tx.Query("SELECT " + iconColumns + " FROM planner_icons WHERE deleted_at IS NULL ORDER BY kind,name,id")
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				v, e := scanIcon(rows)
				if e != nil {
					return e
				}
				items = append(items, v)
			}
			return rows.Err()
		})
		return items, 200, e
	}
	if (id == "" && r.Method != "POST") || (id != "" && r.Method != "PATCH" && r.Method != "DELETE") {
		return nil, 0, methodError(w, "GET, POST, PATCH, DELETE")
	}
	var in struct {
		Name             string `json:"name"`
		Data             []byte `json:"data"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	// JSON base64 shares the application's authenticated request/error handling.
	r.Body = http.MaxBytesReader(w, r.Body, 1500<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(&in); e != nil {
		return nil, 0, badJSON("Некорректный файл или поля иконки")
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return nil, 0, badJSON("Лишние данные")
	}
	in.Name = strings.TrimSpace(in.Name)
	if r.Method != "DELETE" && (utf8.RuneCountInString(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 120) {
		return nil, 0, invalid("Название иконки: 1–120 символов")
	}
	var data []byte
	var width, height int
	var e error
	if id == "" {
		if in.ExpectedRevision != 0 {
			return nil, 0, invalid("expected_revision не используется при загрузке")
		}
		data, width, height, e = normalizeIcon(in.Data)
		if e != nil {
			return nil, 0, e
		}
	} else if len(in.Data) != 0 {
		return nil, 0, invalid("Изображение существующей иконки неизменяемо")
	}
	var result IconDTO
	now := stamp(s.now())
	status := 200
	e = s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		if id == "" {
			sum := fmt.Sprintf("%x", sha256.Sum256(data))
			// A retried upload after a lost response finds the same canonical image.
			result, e = scanIcon(tx.QueryRow("SELECT "+iconColumns+" FROM planner_icons WHERE kind='custom' AND checksum=? AND deleted_at IS NULL", sum))
			if e == nil {
				return nil
			}
			if e != sql.ErrNoRows {
				return e
			}
			var count, size int
			if e = tx.QueryRow("SELECT count(*),COALESCE(sum(length(image)),0) FROM planner_icons WHERE kind='custom'").Scan(&count, &size); e != nil {
				return e
			}
			if count >= 512 || size+len(data) > 32<<20 {
				return invalid("Каталог достиг лимита: 512 изображений или 32 MiB, включая удалённые, сохранённые для истории")
			}
			id, e = operation()
			if e != nil {
				return e
			}
			id = "custom:" + id
			_, e = tx.Exec("INSERT INTO planner_icons(id,name,category,kind,mime,image,checksum,width,height,created_at,updated_at) VALUES(?,?,'Мои иконки','custom','image/png',?,?,?,?,?,?)", id, in.Name, data, sum, width, height, now, now)
			if e != nil {
				return e
			}
			status = 201
			if e = iconEvent(tx, id, "created", now, map[string]any{"name": in.Name, "checksum": sum}); e != nil {
				return e
			}
		} else {
			before, err := scanIcon(tx.QueryRow("SELECT "+iconColumns+" FROM planner_icons WHERE id=?", id))
			if err == sql.ErrNoRows {
				return notFound("Иконка", id)
			}
			if err != nil {
				return err
			}
			if before.Kind != "custom" {
				return invalid("Встроенные иконки нельзя изменять")
			}
			if err = checkRevision(in.ExpectedRevision, before.RecordMeta); err != nil {
				return err
			}
			action := "renamed"
			if r.Method == "DELETE" {
				action = "retired"
				_, e = tx.Exec("UPDATE planner_icons SET deleted_at=?,updated_at=?,revision=revision+1 WHERE id=?", now, now, id)
			} else {
				if before.DeletedAt != nil {
					return invalid("Иконка удалена из каталога")
				}
				_, e = tx.Exec("UPDATE planner_icons SET name=?,updated_at=?,revision=revision+1 WHERE id=?", in.Name, now, id)
			}
			if e != nil {
				return e
			}
			if e = iconEvent(tx, id, action, now, map[string]any{"before": before, "name": in.Name}); e != nil {
				return e
			}
		}
		result, e = scanIcon(tx.QueryRow("SELECT "+iconColumns+" FROM planner_icons WHERE id=?", id))
		return e
	})
	return result, status, e
}

// Validate image bytes as well as SQLite pages before accepting an export/restore.
func checkIconIntegrity(ctx context.Context, q queryer) error {
	rows, e := q.QueryContext(ctx, "SELECT id,kind,image,checksum,width,height FROM planner_icons")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, sum string
		var data []byte
		var width, height int
		if e = rows.Scan(&id, &kind, &data, &sum, &width, &height); e != nil {
			return e
		}
		if kind == "builtin" {
			data, e = builtinIcons.ReadFile("icons/" + strings.TrimPrefix(id, "lucide:") + ".svg")
			if e != nil {
				return fmt.Errorf("unknown built-in icon %s", id)
			}
		} else {
			cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil || format != "png" || cfg.Width != width || cfg.Height != height || width > 1024 || height > 1024 || len(data) > 1<<20 {
				return fmt.Errorf("invalid custom icon %s", id)
			}
			if _, err = png.Decode(bytes.NewReader(data)); err != nil {
				return fmt.Errorf("damaged custom icon %s", id)
			}
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != sum {
			return fmt.Errorf("icon checksum mismatch: %s", id)
		}
	}
	return rows.Err()
}
