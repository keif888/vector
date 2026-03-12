package dsn

// Parse creates a new DSN struct containing the parsed data from the specified string, and a bool as to whether the parsing succeeded.
func Parse(dsn string) (DSN, bool) {
	d := DSN{DSN: dsn}
	p := d.parse()
	return d, p
}
