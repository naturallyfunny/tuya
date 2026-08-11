package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type statement struct {
	sql  string
	args []any
}

type fakeDB struct {
	row      []any
	applied  map[string]bool
	tag      string
	queryErr error
	execErr  error
	queries  []statement
	execs    []statement
}

var _ Querier = (*fakeDB)(nil)

func (f *fakeDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.queries = append(f.queries, statement{sql: sql, args: args})
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if strings.Contains(sql, "tuya_schema_migrations") {
		return &fakeRows{rows: [][]any{{f.applied[args[0].(string)]}}}, nil
	}
	if f.row == nil {
		return &fakeRows{}, nil
	}
	return &fakeRows{rows: [][]any{f.row}}, nil
}

func (f *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execs = append(f.execs, statement{sql: sql, args: args})
	if f.execErr != nil {
		return pgconn.CommandTag{}, f.execErr
	}
	return pgconn.NewCommandTag(f.tag), nil
}

func (f *fakeDB) ran(fragment string) bool {
	for _, s := range f.execs {
		if strings.Contains(s.sql, fragment) {
			return true
		}
	}
	return false
}

func (f *fakeDB) recordedMigrations() []string {
	var versions []string
	for _, s := range f.execs {
		if strings.HasPrefix(s.sql, "INSERT INTO tuya_schema_migrations") {
			versions = append(versions, s.args[0].(string))
		}
	}
	return versions
}

type fakeRows struct {
	rows [][]any
	pos  int
}

var _ pgx.Rows = (*fakeRows)(nil)

func (r *fakeRows) Next() bool {
	if r.pos >= len(r.rows) {
		return false
	}
	r.pos++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.rows[r.pos-1]
	if len(dest) != len(row) {
		return fmt.Errorf("fake rows: %d destinations for a %d column row", len(dest), len(row))
	}
	for i, d := range dest {
		switch d := d.(type) {
		case *string:
			*d = row[i].(string)
		case *int64:
			*d = row[i].(int64)
		case *bool:
			*d = row[i].(bool)
		case *time.Time:
			*d = row[i].(time.Time)
		default:
			return fmt.Errorf("fake rows: unsupported destination %T", d)
		}
	}
	return nil
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
