-- 合并重复的直连上线记录菜单，移除首页复制操作，不改发布事实及首页业务配置。
-- 在配套 Admin/Server 部署前执行；保留已授权角色的同一发布查看入口，不授予新的操作能力。
START TRANSACTION;

INSERT IGNORE INTO admin_role_permissions (role_id, permission_id, created_at)
SELECT assigned.role_id, retained.id, assigned.created_at
FROM admin_role_permissions AS assigned
JOIN admin_permissions AS retired ON retired.id = assigned.permission_id AND retired.code = 'menu.direct_releases'
JOIN admin_permissions AS retained ON retained.code = 'menu.direct_publications';

-- 管理员自定义的子节点保留主键、配置与角色关联，移到仍存在的直连发布菜单下。
UPDATE admin_permissions AS child
JOIN admin_permissions AS retired ON child.parent_id = retired.id
JOIN admin_permissions AS retained ON retained.code = 'menu.direct_publications'
SET child.parent_id = retained.id
WHERE retired.code IN ('menu.direct_releases', 'operations.direct_home.copy');

DELETE assigned FROM admin_role_permissions AS assigned
JOIN admin_permissions AS retired ON retired.id = assigned.permission_id
WHERE retired.code IN ('menu.direct_releases', 'operations.direct_home.copy');

DELETE FROM admin_permissions WHERE code IN ('menu.direct_releases', 'operations.direct_home.copy');

COMMIT;
