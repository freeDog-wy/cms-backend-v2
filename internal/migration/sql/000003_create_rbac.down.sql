BEGIN;

-- 先删除关联表，再删除被引用的表
DROP TABLE role_permissions;
DROP TABLE user_roles;
DROP TABLE permissions;
DROP TABLE roles;

COMMIT;