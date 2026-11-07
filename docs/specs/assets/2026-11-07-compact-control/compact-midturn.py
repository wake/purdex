#!/usr/bin/env python3
# one-off: since env=70 (10-29), how many auto compactions were mid-turn, and how far would the turn have gone
# had nothing compacted it? Reads transcripts only.
import glob, json, os, sys, datetime, statistics, re

SINCE = datetime.datetime(2026, 10, 29, 12, 0).timestamp()
files = []
for pat in ('~/.claude/projects/*/*.jsonl',):
    for f in glob.glob(os.path.expanduser(pat)):
        try:
            if os.path.getmtime(f) >= SINCE:
                files.append(f)
        except OSError:
            pass


def ts(r):
    v = r.get('timestamp')
    if not v:
        return None
    try:
        return datetime.datetime.fromisoformat(v.replace('Z', '+00:00')).timestamp()
    except ValueError:
        return None


def real_user(r):
    """a row a person (or peer) typed: starts a turn"""
    if r.get('type') != 'user' or r.get('isMeta') or r.get('isCompactSummary') or r.get('isSidechain'):
        return False
    c = (r.get('message') or {}).get('content')
    if isinstance(c, str):
        return True
    if isinstance(c, list):
        return not any(isinstance(x, dict) and x.get('type') == 'tool_result' for x in c)
    return False


def used(r):
    if r.get('type') != 'assistant' or r.get('isSidechain'):
        return None
    u = (r.get('message') or {}).get('usage') or {}
    n = (u.get('input_tokens') or 0) + (u.get('cache_read_input_tokens') or 0) + (u.get('cache_creation_input_tokens') or 0)
    return n or None


out = []
for f in files:
    if 'subagents' in f:
        continue
    try:
        raw = open(f, 'rb').read()
    except OSError:
        continue
    if b'compact_boundary' not in raw:
        continue
    rows = []
    for line in raw.splitlines():
        try:
            rows.append(json.loads(line))
        except ValueError:
            pass
    for i, r in enumerate(rows):
        if r.get('subtype') != 'compact_boundary':
            continue
        t = ts(r)
        if t is None or t < SINCE:
            continue
        md = r.get('compactMetadata') or {}
        trig = md.get('trigger')
        pre = md.get('preTokens')
        # idle: the marker follows or precedes within a few rows
        near = rows[max(0, i - 3):i + 6]
        idle = any('Compacted while idle' in json.dumps(x.get('content', '')) for x in near if x.get('type') == 'system')
        # mid-turn: the next meaningful row after the summary is not a person's prompt, and the agent goes on working
        j = i + 1
        while j < len(rows) and (rows[j].get('isCompactSummary') or rows[j].get('isMeta') or rows[j].get('type') in ('system', 'attachment', 'file-history-snapshot', 'summary', 'progress', 'custom-title', 'ai-title', 'last-prompt', 'permission-mode', 'queue-operation')):
            j += 1
        mid = False
        growth = 0
        after0 = None
        calls = 0
        if j < len(rows) and not real_user(rows[j]):
            mid = True
        if mid:
            # walk to the end of that turn: how many requests, how much the context grew
            for k in range(j, len(rows)):
                if real_user(rows[k]) or rows[k].get('subtype') == 'compact_boundary':
                    break
                n = used(rows[k])
                if n:
                    calls += 1
                    if after0 is None:
                        after0 = n
                    growth = max(growth, n - after0)
        win = 1000000 if (pre or 0) > 200000 else 200000
        # how long the session had been quiet before the boundary: an idle compaction comes about 54 minutes after the last answer
        last = None
        for k in range(i - 1, max(-1, i - 400), -1):
            if rows[k].get('type') == 'assistant' and not rows[k].get('isSidechain') and ts(rows[k]):
                last = ts(rows[k])
                break
        gap = (t - last) if last else None
        out.append(dict(file=os.path.basename(f)[:8], t=t, trig=trig, pre=pre, idle=idle, mid=mid, calls=calls, growth=growth, win=win, gap=gap, after0=after0))

out.sort(key=lambda x: x['t'])
print('sessions scanned', len(files), 'boundaries', len(out))
is_idle = lambda x: x['trig'] == 'auto' and (x['idle'] or (x['gap'] or 0) > 2400)
is_mid = lambda x: x['trig'] == 'auto' and not is_idle(x) and x['calls'] > 0
for key, sel in (('manual', lambda x: x['trig'] == 'manual'), ('idle (gap>40m)', is_idle), ('auto, turn went on', is_mid), ('auto, turn ended there', lambda x: x['trig'] == 'auto' and not is_idle(x) and x['calls'] == 0)):
    xs = [x for x in out if sel(x)]
    print(f'{key:24s} {len(xs):4d}  median preTokens {int(statistics.median([x["pre"] or 0 for x in xs])) if xs else 0}')
mid = [x for x in out if is_mid(x)]
if mid:
    extra = sum(x['calls'] * max(0, (x['pre'] or 0) - (x['after0'] or 0)) for x in mid)
    print('extra cached-context tokens re-read had these turns run to their end uncompacted:', extra)
    print('total requests left over all these turns:', sum(x['calls'] for x in mid))
    print('mid-turn: requests left in that turn   median', statistics.median([x['calls'] for x in mid]), 'p90', sorted(x['calls'] for x in mid)[int(len(mid) * .9)], 'max', max(x['calls'] for x in mid))
    print('mid-turn: growth after compaction      median', int(statistics.median([x['growth'] for x in mid])), 'p90', sorted(x['growth'] for x in mid)[int(len(mid) * .9)], 'max', max(x['growth'] for x in mid))
    over = [x for x in mid if (x['pre'] or 0) + x['growth'] > x['win'] * .97]
    print('mid-turn: would pass 97% anyway (pre + growth)', len(over), 'of', len(mid))
    print('windows', {w: sum(1 for x in mid if x['win'] == w) for w in (200000, 1000000)})
    print('top by requests left:')
    for x in sorted(mid, key=lambda x: -x['calls'])[:8]:
        print(' ', x['file'], datetime.datetime.fromtimestamp(x['t']).strftime('%m-%d %H:%M'), 'pre', x['pre'], 'calls', x['calls'], 'growth', x['growth'])
