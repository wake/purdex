#!/usr/bin/env python3
# one-off: in real transcripts since the 781 deploy, what did the model answer right after each injected record?
import glob, json, os, datetime
S = datetime.datetime(2026, 11, 7, 13, 45).timestamp()
def ts(r):
    v = r.get('timestamp')
    try: return datetime.datetime.fromisoformat(v.replace('Z', '+00:00')).timestamp() if v else None
    except ValueError: return None
def text(r):
    c = (r.get('message') or {}).get('content')
    if isinstance(c, str): return c
    if isinstance(c, list): return ' '.join(x.get('text', '') or '' for x in c if isinstance(x, dict))
    return ''
for f in glob.glob(os.path.expanduser('~/.claude/projects/*/*.jsonl')):
    if os.path.getmtime(f) < S or 'accept-781' in f or 'compact-ctl-probe' in f or 'compact-hold-spike' in f: continue
    raw = open(f, 'rb').read()
    if b'injected by Purdex after compaction' not in raw: continue
    rows = [json.loads(l) for l in raw.splitlines() if l.strip()]
    cwd = next((r.get('cwd') for r in rows if r.get('cwd')), '?')
    for i, r in enumerate(rows):
        if r.get('isMeta') and 'injected by Purdex after compaction' in text(r) and (ts(r) or 0) >= S:
            nxt_user = next((rows[k] for k in range(i + 1, len(rows)) if rows[k].get('type') == 'user' and not rows[k].get('isMeta') and text(rows[k]).strip() and 'tool_result' not in json.dumps((rows[k].get('message') or {}).get('content'))[:60]), None)
            nxt_agent = next((rows[k] for k in range(i + 1, len(rows)) if rows[k].get('type') == 'assistant' and text(rows[k]).strip()), None)
            print('==', os.path.basename(f)[:8], cwd[-40:], datetime.datetime.fromtimestamp(ts(r)).strftime('%H:%M:%S'))
            print('   next user :', repr(text(nxt_user)[:120]) if nxt_user else None)
            print('   next agent:', repr(text(nxt_agent)[:200]) if nxt_agent else None)
