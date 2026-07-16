# Query Plan Baselines

CI runs `query-plan-baselines.sql` after migrations and uploads the resulting JSON
plans as an artifact. Re-run the same file against a staging database with
production-shaped data after every index, pagination, or query-shape change. The
commands intentionally use `EXPLAIN (ANALYZE, BUFFERS)` so planning, execution,
and cache behavior are all visible.

## Announcements

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT a.id,
       vr.user_id IS NOT NULL AS read,
       COALESCE(rc.read_count, 0) AS read_count
FROM announcements a
LEFT JOIN announcement_reads vr
  ON vr.announcement_id = a.id AND vr.user_id = 42
LEFT JOIN (
  SELECT announcement_id, count(*) AS read_count
  FROM announcement_reads
  GROUP BY announcement_id
) rc ON rc.announcement_id = a.id
WHERE a.audience = 'all'
ORDER BY a.published_at DESC
LIMIT 20 OFFSET 0;
```

## Listing Search And Seller Summary

```sql
EXPLAIN (ANALYZE, BUFFERS)
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
```

## Feed

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT e.id, e.type, e.created_at
FROM content_entities e
WHERE e.publication_status = 'published'
  AND e.moderation_status = 'approved'
ORDER BY e.created_at DESC
LIMIT 20 OFFSET 0;
```

The acceptance criteria are an index-supported bounded page query, no per-row
announcement-read query, no unbounded sort or sequential scan on a large search
table, and buffer growth that remains proportional to the requested page size.
