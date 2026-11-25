-- 新增独立只读报表菜单，不自动扩大已有角色权限；重复执行保留自定义名称和层级。
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'投放链接奖励贡献','menu.campaign_report','menu','/channels/campaign-report',30,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)
FROM (SELECT id FROM admin_permissions WHERE code='directory.operations') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'查看投放链接奖励贡献','operations.campaign_report.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)
FROM (SELECT id FROM admin_permissions WHERE code='menu.campaign_report') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;
