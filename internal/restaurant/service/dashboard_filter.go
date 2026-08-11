package service

// DashboardFilter acota métricas al usuario operativo (cajero) o muestra toda la sucursal (admin).
type DashboardFilter struct {
	UserID       uint
	RestrictUser bool
}

func (f DashboardFilter) salesUserClause(tableAlias string) (clause string, arg interface{}) {
	if !f.RestrictUser || f.UserID == 0 {
		return "", nil
	}
	if tableAlias != "" {
		return tableAlias + ".user_id = ?", f.UserID
	}
	return "user_id = ?", f.UserID
}
