-- 新建日表继承按应用与类型排列的覆盖索引，已有日表须在发布查询改写前执行 upgrade-metric-behavior-index.sql。
-- InnoDB 二级索引隐含完整主键，已经覆盖完成匹配所需的 user_id 和 event_id，无需重复列入。
ALTER TABLE `metric_events_template`
  ADD INDEX `idx_metrics_app_kind_received` (`application_id`,`kind`,`received_at`);
