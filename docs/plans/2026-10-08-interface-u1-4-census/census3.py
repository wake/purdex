# Third census: field availability by CC version for prompt rows; turn_duration vs prompts per main file; promptId grouping.
import json, os, glob, collections, time
root = os.path.expanduser('~/.claude/projects')
cutoff = time.time() - 30*86400
files = [f for f in glob.glob(root+'/*/*.jsonl') if os.path.getmtime(f) > cutoff]  # main transcripts only
C = collections.Counter
byver = collections.defaultdict(C)
pairs = C(); promptid_rows = C(); tp_monot = C(); td_after = C(); stop_end = C()
for f in files:
    rows=[]
    for line in open(f,'rb'):
        try: r=json.loads(line)
        except Exception: continue
        if isinstance(r,dict): rows.append(r)
    last_ti=None; prompts=0; tds=0
    for i,r in enumerate(rows):
        t=r.get('type'); m=r.get('message') if isinstance(r.get('message'),dict) else {}
        if t=='user' and not r.get('isMeta') and 'toolUseResult' not in r:
            c=m.get('content')
            istr = isinstance(c,list) and any(isinstance(b,dict) and b.get('type')=='tool_result' for b in c)
            if istr: continue
            v=r.get('version','?'); v=v[:7]
            byver[v]['prompts']+=1
            for k in ('promptId','promptSource','turnOrigin','origin','turnPosition','permissionMode'):
                if k in r: byver[v][k]+=1
            pairs[(r.get('turnOrigin'), r.get('promptSource'), (r.get('origin') or {}).get('kind') if isinstance(r.get('origin'),dict) else None)]+=1
            if 'turnPosition' in r:
                ti=r['turnPosition'].get('turnIndex')
                if last_ti is not None: tp_monot['+1' if ti==last_ti+1 else ('same' if ti==last_ti else 'other')]+=1
                last_ti=ti
            prompts+=1
        if t=='system' and r.get('subtype')=='turn_duration': tds+=1
        if t=='user' and 'promptId' in r: promptid_rows[r['promptId']]+=1
    if prompts: td_after['ratio_bucket:'+('0' if tds==0 else ('<0.5' if tds/prompts<0.5 else ('<1' if tds/prompts<1 else '>=1')))]+=1
print('== prompt-row field presence by version (count/prompts)')
for v in sorted(byver):
    p=byver[v]['prompts']; print('  ',v,p,' '.join(f"{k}={byver[v][k]}" for k in ('promptId','promptSource','turnOrigin','origin','turnPosition','permissionMode')))
print('== (turnOrigin, promptSource, origin.kind)')
for k,v in pairs.most_common(30): print(f'   {v:6d}  {k}')
print('== turnIndex step between consecutive prompt rows', dict(tp_monot))
print('== turn_duration per prompt ratio per file', dict(td_after))
print('== rows sharing a promptId (bucket)', dict(C(min(v,6) for v in promptid_rows.values())))
