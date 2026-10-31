// ---------- toolsB: shared helper for the calls-in-flight, nearby-activity and changes fixes (S1B) ----------
function TBWindowMs(s) {
  var m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(String(s));
  if (!m) return NaN;
  return Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]));
}
