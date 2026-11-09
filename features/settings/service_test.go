package settings

import "testing"

func TestPublicAPIPointsToSnapshotRuntime(t *testing.T) {
	got := NewService(Config{}).Get().PublicAPI
	if got.RuntimePath != "/api/v2/runtime?path={path}" {
		t.Fatalf("runtime path = %q", got.RuntimePath)
	}
	if got.FormsPath != "/api/v2/forms/{key}/submissions" {
		t.Fatalf("forms path = %q", got.FormsPath)
	}
	if got.MediaPath != "/media/{id}" {
		t.Fatalf("media path = %q", got.MediaPath)
	}
}

func TestRoleMatrixMatchesBranchContentSubmissionAndMediaRoutes(t *testing.T) {
	roles := NewService(Config{}).Get().Roles
	byID := make(map[string]Role, len(roles))
	for _, role := range roles {
		byID[role.ID] = role
	}
	if len(byID) != 4 {
		t.Fatalf("role matrix must expose admin, moderator, staff and user; got %v", roleIDs(roles))
	}

	assertActions := func(roleID, resource string, want []string) {
		t.Helper()
		role, ok := byID[roleID]
		if !ok {
			t.Fatalf("missing role %q", roleID)
		}
		for _, permission := range role.Permissions {
			if permission.Resource == resource {
				if !sameActions(permission.Actions, want) {
					t.Errorf("%s/%s actions = %v, want %v", roleID, resource, permission.Actions, want)
				}
				return
			}
		}
		t.Fatalf("role %q does not document resource %q", roleID, resource)
	}
	managers := []string{"admin", "moderator"}
	editors := []string{"admin", "moderator", "staff", "user"}
	for _, role := range managers {
		assertActions(role, "Сайт", []string{"редактирование снапшотов", "назначение снапшотов веткам", "удаление незакреплённых снапшотов"})
		wantSubmissions := []string{"просмотр", "смена статуса", "архивирование"}
		if role == "admin" {
			wantSubmissions = append(wantSubmissions, "удаление")
		}
		assertActions(role, "Заявки", wantSubmissions)
	}
	for _, role := range editors {
		assertActions(role, "Контент", []string{"просмотр", "создание", "редактирование", "публикация", "восстановление истории"})
		wantMedia := []string{"просмотр", "загрузка", "переименование"}
		if role == "admin" {
			wantMedia = append(wantMedia, "удаление")
		}
		assertActions(role, "Медиа", wantMedia)
	}
	assertActions("staff", "Сайт", []string{})
	assertActions("user", "Сайт", []string{})
	assertActions("staff", "Заявки", []string{"просмотр", "экспорт"})
	assertActions("user", "Заявки", []string{"просмотр", "экспорт"})
}

func roleIDs(roles []Role) []string {
	ids := make([]string, 0, len(roles))
	for _, role := range roles {
		ids = append(ids, role.ID)
	}
	return ids
}

func sameActions(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
