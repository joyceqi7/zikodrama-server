-- 审核历史改为官方发布详情的只读入口；只移除独立菜单，不修改任务或审核事实。
-- 不把旧菜单授权转成官方发布授权，避免扩大仅有历史查看权限的角色范围。
START TRANSACTION;

DELETE assigned FROM admin_role_permissions AS assigned
JOIN admin_permissions AS retired ON retired.id = assigned.permission_id
WHERE retired.code = 'menu.review_records';

-- 无自定义子节点时直接删除；有子节点则保留原目录容器及启停状态，避免搬迁使停用子树重新生效。
DELETE retired FROM admin_permissions AS retired
LEFT JOIN admin_permissions AS child ON child.parent_id = retired.id
WHERE retired.code = 'menu.review_records' AND child.id IS NULL;

UPDATE admin_permissions
SET type = 'directory', route = '', updated_at = CURRENT_TIMESTAMP(6)
WHERE code = 'menu.review_records' AND type = 'menu';

COMMIT;
