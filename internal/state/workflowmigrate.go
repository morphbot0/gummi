package state

import (
	"bufio"
	"context"
	"database/sql"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// The move from crews to workflows (DESIGN §25) as the database sees it: a
// session's one spec becomes spec 1 of its list, its revisions are
// numbered per spec, and any crew still open is stopped.

// workflowsVersion is the user_version a database carries once
// migrateToWorkflows has run on it. The step runs once: a crew a later
// gummi starts is not stopped on the next open.
const workflowsVersion = 1

// rebuildSpecRevisions moves spec_revisions keyed (feature_id, rev) to the
// key that also holds the spec's number, every existing row on spec 1.
// Same transactional rebuild as rebuildStageSpendPK: SQLite cannot alter a
// primary key in place. Idempotent — a table that already has spec_n is
// left alone, which is every fresh database.
func rebuildSpecRevisions(db *sql.DB) error {
	ctx := context.Background()
	var has bool
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) > 0 FROM pragma_table_info('spec_revisions') WHERE name = 'spec_n'`,
	).Scan(&has); err != nil {
		return err
	}
	if has {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	for _, stmt := range []string{
		`CREATE TABLE spec_revisions_new (
			feature_id TEXT NOT NULL REFERENCES features(id) ON DELETE CASCADE,
			spec_n     INTEGER NOT NULL DEFAULT 1,
			rev        INTEGER NOT NULL,
			content    TEXT NOT NULL,
			by         TEXT NOT NULL DEFAULT '',
			at         TEXT NOT NULL,
			PRIMARY KEY (feature_id, spec_n, rev)
		)`,
		`INSERT INTO spec_revisions_new (feature_id, spec_n, rev, content, by, at)
		 SELECT feature_id, 1, rev, content, by, at FROM spec_revisions`,
		`DROP TABLE spec_revisions`,
		`ALTER TABLE spec_revisions_new RENAME TO spec_revisions`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// workspaceRootOf is the workspace root a store at dbPath belongs to —
// <root>/.gummi/state/<db> — or "" when dbPath is not laid out that way
// (a test's bare database).
func workspaceRootOf(dbPath string) string {
	state := filepath.Dir(dbPath)
	gummi := filepath.Dir(state)
	if filepath.Base(state) != "state" || filepath.Base(gummi) != ".gummi" {
		return ""
	}
	return filepath.Dir(gummi)
}

// migrateToWorkflows runs once per database (user_version): it registers
// each session's existing spec as its spec 1, at the path it has always
// had (Feature.SpecPath), and stops every crew still open (§25.2) — their
// hidden cards stay hidden, and nothing runs them again.
//
// A session has a spec when its file is on disk under root; with no root
// to look under, when it has a revision in the store. A session with
// neither gets no row, and revisions it has (its file is gone) move to
// spec 0, which no spec is: they are kept, and a spec it starts later
// begins at revision 1 with none of them under it.
//
// Two gummis may open an old database at once. The version is read again
// inside the transaction, which takes the write lock at BEGIN
// (_txlock=immediate), so the second finds the first's work done.
func migrateToWorkflows(db *sql.DB, root string) error {
	ctx := context.Background()
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= workflowsVersion {
		return nil
	}
	if beforeWorkflowsMigration != nil {
		beforeWorkflowsMigration()
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= workflowsVersion {
		return nil // another gummi migrated it while this one waited
	}
	sessions, err := unregisteredSessions(ctx, tx)
	if err != nil {
		return err
	}
	for _, s := range sessions {
		rel := path.Join(".gummi", "specs", s.id+"-"+s.slug+".md")
		title, created := s.title, s.created
		var first sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT MIN(at) FROM spec_revisions WHERE feature_id = ?`, s.id).Scan(&first); err != nil {
			return err
		}
		if root != "" {
			heading, ok := specHeading(filepath.Join(root, filepath.FromSlash(rel)))
			if !ok {
				if _, err := tx.ExecContext(ctx, `UPDATE spec_revisions SET spec_n = 0 WHERE feature_id = ? AND spec_n = 1`, s.id); err != nil {
					return err
				}
				continue
			}
			if heading != "" {
				title = heading
			}
		} else if !first.Valid {
			continue
		}
		if first.Valid {
			created = first.String
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_specs (feature_id, n, title, path, created_at) VALUES (?,1,?,?,?)`,
			s.id, title, rel, created); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE crews SET state = ?, pending = '', stopping = 0,
		report = CASE WHEN report = '' THEN ? ELSE report END, updated_at = ?
		WHERE state IN (?,?,?,?)`,
		legacyCrewPartial, crewStoppedOnUpgrade, time.Now().UTC().Format(timeFmt),
		legacyCrewProposed, legacyCrewRunning, legacyCrewPaused, legacyCrewReviewing); err != nil {
		return err
	}
	// PRAGMA user_version is transactional: a migration that fails here
	// runs again on the next open.
	if _, err := tx.ExecContext(ctx, `PRAGMA user_version = 1`); err != nil {
		return err
	}
	return tx.Commit()
}

// beforeWorkflowsMigration, when a test sets it, runs between the first
// read of the version and the migration's transaction: the moment another
// gummi may migrate the same database.
var beforeWorkflowsMigration func()

// legacySession is a session the upgrade may register a spec 1 for.
type legacySession struct{ id, slug, title, created string }

// unregisteredSessions lists the sessions — freeform cards no crew owns —
// with no spec registered yet.
func unregisteredSessions(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
},
) ([]legacySession, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, slug, title, created_at FROM features
		WHERE kind = ? AND crew_seq = 0
		AND NOT EXISTS (SELECT 1 FROM session_specs WHERE session_specs.feature_id = features.id)`,
		string(domain.KindFreeform))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacySession
	for rows.Next() {
		var s legacySession
		if err := rows.Scan(&s.id, &s.slug, &s.title, &s.created); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// crewStoppedOnUpgrade is the report of a crew that was open when gummi
// moved to workflows.
const crewStoppedOnUpgrade = "Stopped when gummi replaced crews with workflows: nothing of it runs again. Its branch is left as it was."

// specHeading reads a spec file's "# " title; ok is false when there is
// no file.
func specHeading(file string) (string, bool) {
	f, err := os.Open(file)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if t, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(t), true
		}
		return "", true
	}
	return "", true
}
