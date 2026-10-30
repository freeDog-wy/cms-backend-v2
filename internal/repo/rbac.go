package repo

import "context"

//go:generate go run gorm.io/cli/gorm gen -i $GOFILE -o ./generated

type RBACRepo[T any] interface {
	// 检查当前用户、角色和权限，禁用用户或角色均不能通过。
	//
	// SELECT EXISTS (
	// SELECT 1 FROM users u
	// JOIN user_roles ur ON ur.user_id = u.id
	// JOIN roles r ON r.id = ur.role_id AND r.status = 1
	// JOIN role_permissions rp ON rp.role_id = r.id
	// JOIN permissions p ON p.id = rp.permission_id
	// WHERE u.id = @userID AND u.status = 1 AND p.code = @permission
	// )
	HasPermission(ctx context.Context, userID int64, permission string) (bool, error)

	// SELECT DISTINCT p.code FROM permissions p
	// JOIN role_permissions rp ON rp.permission_id = p.id
	// JOIN roles r ON r.id = rp.role_id AND r.status = 1
	// JOIN user_roles ur ON ur.role_id = r.id
	// JOIN users u ON u.id = ur.user_id AND u.status = 1
	// WHERE u.id = @userID ORDER BY p.code
	ListPermissions(ctx context.Context, userID int64) ([]string, error)

	// 初始化时补充角色，不覆盖已有角色的状态。
	//
	// INSERT INTO roles (code, name) VALUES ('admin', '管理员')
	// ON CONFLICT (code) DO NOTHING
	EnsureAdminRole(ctx context.Context) error

	// INSERT INTO permissions (code, name) VALUES ('comment:moderate', '管理评论')
	// ON CONFLICT (code) DO NOTHING
	EnsureCommentPermission(ctx context.Context) error

	// INSERT INTO role_permissions (role_id, permission_id)
	// SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
	// WHERE r.code = 'admin' AND p.code = 'comment:moderate'
	// ON CONFLICT DO NOTHING
	GrantAdminCommentPermission(ctx context.Context) error

	// INSERT INTO user_roles (user_id, role_id)
	// SELECT @userID, id FROM roles WHERE code = 'admin'
	// ON CONFLICT DO NOTHING
	AssignAdmin(ctx context.Context, userID int64) error
}
