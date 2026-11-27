#!/bin/sh
# One-line downloader status: done today, active now, waiting, failed, free disk.
cd "$(dirname "$0")" || exit 1
python3 - <<'PY'
import json, re, shutil, datetime, os
now = datetime.datetime.now()
today = now.strftime("%Y/%m/%d")
done, active = 0, set()
try:
    for l in open("macdrop.log", errors="ignore").readlines()[-20000:]:
        if l.startswith(today) and "download OK" in l: done += 1
        m = re.match(r"^(\d{4}/\d\d/\d\d \d\d:\d\d:\d\d)\.\d+ progress msg=(\d+)", l)
        if m and (now - datetime.datetime.strptime(m.group(1), "%Y/%m/%d %H:%M:%S")).total_seconds() < 15:
            active.add(m.group(2))
except FileNotFoundError:
    pass
try:
    st = json.load(open("failed.json"))
except Exception:
    st = {}
failed = len(st.get("Failed") or {})
pend = st.get("Pending") or {}
wait = f"waiting {max(0, len(pend) - len(active))} ({sum((t['A'].get('file_size') or 0) for t in pend.values())/1e9:.1f} GB)" if pend else "waiting: see Inngest"
free = shutil.disk_usage(os.path.expanduser("~")).free / 1e9
print(f"{now:%H:%M} · done today {done} · downloading {len(active)} · {wait} · failed {failed} · disk free {free:.0f} GB")
PY
