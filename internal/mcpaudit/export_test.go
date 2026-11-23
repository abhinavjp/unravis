package mcpaudit

// ExecForTest runs one statement on the store (tests only: they fill the table faster than Record can).
func ExecForTest(d *DB, q string, args ...any) (int64, error) {
	r, err := d.sql.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}
