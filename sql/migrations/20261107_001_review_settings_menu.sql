-- 审核开关独立菜单；在 mini_review 增量后执行，不改写已有角色授权。
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='directory.whitelist'),'审核开关','menu.review_settings','menu','/whitelist/review-settings',10,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='menu.review_settings'),'查看审核开关','operations.review_settings.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
VALUES ((SELECT p.id FROM (SELECT id,code FROM admin_permissions) AS p WHERE p.code='menu.review_settings'),'修改审核开关','operations.review_settings.update','action','',10,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=id;
