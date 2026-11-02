package orm

// visibleClause renders the predicates that hide logically deleted rows.
//
// A model either deletes with a timestamp column (soft_delete, hidden once it stops
// being NULL) or with a flag (retain, hidden once it is true); ModelOf refuses a model
// that declares both. A model that declares neither yields no clause, so the generated
// SQL stays exactly as before for tables that do not need the guard.
func visibleClause(model *Model, dialect Dialect, index int) (string, []any, error) {
	switch {
	case model.SoftDelete != nil:
		return dialect.Quote(model.SoftDelete.Column) + " IS NULL", nil, nil
	case model.Retain != nil:
		return dialect.Quote(model.Retain.Column) + " = " + dialect.Placeholder(index), []any{false}, nil
	default:
		return "", nil, nil
	}
}

// softDelete reports whether Delete must hide the row instead of removing it.
func (m *Model) softDelete() bool { return m.SoftDelete != nil || m.Retain != nil }
