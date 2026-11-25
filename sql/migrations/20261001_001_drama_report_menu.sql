-- 新增运营数据只读报表菜单与独立操作权限；已有角色不会自动获得新增权限。
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'剧目播放与解锁','menu.drama_report','menu','/channels/drama-report',20,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)
FROM (SELECT id FROM admin_permissions WHERE code='directory.operations') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'查看剧目播放与解锁','operations.drama_report.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)
FROM (SELECT id FROM admin_permissions WHERE code='menu.drama_report') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;
