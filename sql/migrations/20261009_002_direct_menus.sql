-- 新增菜单与精确动作，不扩展已有角色授权；重复执行保留自定义名称和菜单主键。

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT NULL,'TikTok 官方运营','directory.official','directory','',40,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT NULL,'VePlayer 直连运营','directory.direct','directory','',45,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'官方发布','menu.official_publications','menu','/official/publications',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='directory.official') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连发布','menu.direct_publications','menu','/direct/publications',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='directory.direct') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连上线记录','menu.direct_releases','menu','/direct/releases',10,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='directory.direct') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连分类','menu.direct_category_rankings','menu','/direct/category-rankings',20,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='directory.direct') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连轮播','menu.direct_carousel','menu','/direct/carousel',30,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='directory.direct') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连热播','menu.direct_hot','menu','/direct/hot-dramas',40,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='directory.direct') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连发布查看','operations.direct_publication.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_publications') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连发布新版本','operations.direct_publication.publish','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_publications') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连全渠道下架','operations.direct_publication.offline','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_publications') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连恢复上架','operations.direct_publication.restore','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_publications') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连回滚版本','operations.direct_publication.rollback','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_publications') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连应用关系查看','operations.direct_distribution.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_publications') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连应用关系修改','operations.direct_distribution.update','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_publications') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连轮播查看','operations.direct_carousel.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_carousel') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连轮播修改','operations.direct_carousel.update','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_carousel') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连热播查看','operations.direct_hot.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_hot') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连热播修改','operations.direct_hot.update','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_hot') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连分类查看','operations.direct_ranking.read','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_category_rankings') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'直连分类修改','operations.direct_ranking.update','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_category_rankings') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'复制官方首页配置','operations.direct_home.copy','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.direct_publications') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'修改直连配置引用','application.playback.update','action','',0,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='menu.applications') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

UPDATE admin_permissions child JOIN admin_permissions old_parent ON child.parent_id=old_parent.id JOIN admin_permissions new_parent ON new_parent.code='directory.official' SET child.parent_id=new_parent.id WHERE child.code IN ('menu.review_records','menu.release_records','menu.carousel','menu.hot','menu.categoryrankings') AND old_parent.code IN ('directory.content','menu.dramas');

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'官方审核记录','menu.review_records','menu','/dramas/reviews',10,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='directory.official') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;

INSERT INTO admin_permissions (parent_id,name,code,type,route,sort_order,status,created_at,updated_at)
SELECT parent.id,'官方上线记录','menu.release_records','menu','/dramas/releases',20,'active',CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6) FROM (SELECT id FROM admin_permissions WHERE code='directory.official') AS parent
ON DUPLICATE KEY UPDATE id=admin_permissions.id;
