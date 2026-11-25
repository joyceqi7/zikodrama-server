-- 直连上下架回到短剧管理；保留操作节点主键和已有精确授权，不修改渠道发布数据。
-- 前置条件：menu.dramas 已存在。旧接口及 ready 确认接口仍兼容，新的发布动作不再依赖 ready。
START TRANSACTION;

-- 记录旧入口整条祖先链的启停状态，移动后不能激活此前被父节点停用的能力。
WITH RECURSIVE source_ancestors AS (
  SELECT id,parent_id,status FROM admin_permissions WHERE code='menu.direct_publications'
  UNION ALL
  SELECT parent.id,parent.parent_id,parent.status FROM admin_permissions parent
  JOIN source_ancestors child ON child.parent_id=parent.id
)
SELECT COALESCE(MIN(status='active'),1) INTO @direct_merge_source_active FROM source_ancestors;

-- 只有同时已获内容读取权限的旧菜单使用者获得内容菜单入口；不新增任何业务读取或写入权限。
INSERT IGNORE INTO admin_role_permissions(role_id,permission_id,created_at)
SELECT old_grant.role_id,target.id,old_grant.created_at
FROM admin_role_permissions old_grant
JOIN admin_permissions source ON source.id=old_grant.permission_id AND source.code='menu.direct_publications'
JOIN admin_permissions target ON target.code='menu.dramas'
WHERE @direct_merge_source_active=1 AND EXISTS (
  SELECT 1 FROM admin_role_permissions read_grant JOIN admin_permissions readable ON readable.id=read_grant.permission_id
  WHERE read_grant.role_id=old_grant.role_id AND readable.code='content.drama.read' AND readable.status='active'
);

-- 原操作与自定义子树一起迁移；旧祖先停用时停用子树根，保留所有原角色关联。
UPDATE admin_permissions child
JOIN admin_permissions source ON source.id=child.parent_id AND source.code='menu.direct_publications'
JOIN admin_permissions target ON target.code='menu.dramas'
SET child.parent_id=target.id,child.status=IF(@direct_merge_source_active=1,child.status,'disabled'),child.updated_at=CURRENT_TIMESTAMP(6);

DELETE assigned FROM admin_role_permissions assigned
JOIN admin_permissions source ON source.id=assigned.permission_id AND source.code='menu.direct_publications'
WHERE EXISTS(SELECT 1 FROM admin_permissions target WHERE target.code='menu.dramas');

DELETE source FROM admin_permissions source
JOIN admin_permissions target ON target.code='menu.dramas'
LEFT JOIN admin_permissions child ON child.parent_id=source.id
WHERE source.code='menu.direct_publications' AND child.id IS NULL;

COMMIT;
