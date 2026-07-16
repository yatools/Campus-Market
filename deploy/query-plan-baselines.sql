-- Generated during CI after migrations. Keep the statements aligned with the
-- production query shapes documented in query-plan-baselines.md.
EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT a.id,
       vr.user_id IS NOT NULL AS read,
       COALESCE(rc.read_count, 0) AS read_count
FROM announcements a
LEFT JOIN announcement_reads vr
  ON vr.announcement_id = a.id AND vr.user_id = 0
LEFT JOIN (
  SELECT announcement_id, count(*) AS read_count
  FROM announcement_reads
  GROUP BY announcement_id
) rc ON rc.announcement_id = a.id
WHERE a.audience = 'all'
ORDER BY a.published_at DESC
LIMIT 20 OFFSET 0;

EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT e.id, l.title, l.price_cents,
       (SELECT count(*) FROM market_transactions mt
        WHERE mt.seller_id = e.owner_id AND mt.status = 'completed') AS completed_sales
FROM content_entities e
JOIN listings l ON l.entity_id = e.id
WHERE e.publication_status = 'published'
  AND e.moderation_status = 'approved'
  AND (l.title ILIKE '%laptop%' OR l.description ILIKE '%laptop%')
ORDER BY e.created_at DESC
LIMIT 20 OFFSET 0;

EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT e.id, e.type, e.created_at
FROM content_entities e
WHERE e.publication_status = 'published'
  AND e.moderation_status = 'approved'
ORDER BY e.created_at DESC
LIMIT 20 OFFSET 0;
