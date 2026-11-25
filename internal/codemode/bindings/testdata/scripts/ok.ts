export async function main() {
  const list = await unravis.audit_list_sources();
  if ("error" in list && list.error) { return list.error.code; }
  const ids: string[] = list.sources!.map(s => s.id);
  const cmp = await unravis.audit_compare({ query: {
    sourceIds: ids, start: "2026-11-01T00:00:00Z", end: "2026-11-02T00:00:00Z", baselineStart: "2026-10-01T00:00:00Z", baselineEnd: "2026-10-02T00:00:00Z",
    kind: "all", outcome: "all", metric: "total", bucketSeconds: 300 } });
  if (cmp.error) { return cmp.error.message; }
  const top = cmp.groups![0];
  const p95: number | null = top.p95DeltaMs;
  const id: string = cmp.resultId!;
  const page = await unravis.audit_get_requests({ resultId: id, apiKey: top.key, limit: 10 });
  if (page.error) { return page.error.code; }
  const first = page.rows![0];
  const ms: number | null = first.durationMs;
  const rid: string = first.id;
  const bd = await unravis.audit_get_breakdown({ resultId: id, dim: "statusCode" });
  const kind: "shown" | "hidden" | undefined = bd.identifiers;
  return { p95, ms, rid, kind, count: bd.rows?.[0]?.count };
}
