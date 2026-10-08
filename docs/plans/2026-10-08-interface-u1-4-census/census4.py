# Fourth census: resume/handoff signals and subagent linkage. Counts only.
import json, os, glob, collections, time
root = os.path.expanduser('~/.claude/projects')
cutoff = time.time() - 30*86400
files = [f for f in glob.glob(root+'/*/*.jsonl') if os.path.getmtime(f) > cutoff]
C = collections.Counter
E = collections.defaultdict(C)
for f in files:
    stem = os.path.basename(f)[:-6]
    rows=[]
    for line in open(f,'rb'):
        try: r=json.loads(line)
        except Exception: continue
        if isinstance(r,dict): rows.append(r)
    sids=[r.get('sessionId') for r in rows if r.get('type') in ('user','assistant','system','attachment')]
    other=[i for i,s in enumerate(sids) if s and s!=stem]
    if other:
        E['files with foreign sessionId rows']['n']+=1
        E['foreign position']['all-before-own' if all(i < next((j for j,s in enumerate(sids) if s==stem), len(sids)) for i in other) else 'mixed']+=1
    eps=[r.get('entrypoint') for r in rows if r.get('entrypoint')]
    runs=sum(1 for a,b in zip(eps,eps[1:]) if a!=b)
    E['entrypoint switches per file'][min(runs,5)]+=1
    uuids=C(r.get('uuid') for r in rows if r.get('uuid'))
    E['duplicate uuid in file'][('yes' if any(v>1 for v in uuids.values()) else 'no')]+=1
    sub = os.path.join(f[:-6], 'subagents')
    agentids=set()
    for r in rows:
        tu=r.get('toolUseResult')
        if isinstance(tu,dict) and tu.get('agentId'): agentids.add(tu['agentId'])
    if os.path.isdir(sub):
        have=set(n[len('agent-'):-6] for n in os.listdir(sub) if n.endswith('.jsonl'))
        E['subagent link'][f'toolUseResult.agentId found as file']+=len(agentids & have)
        E['subagent link']['agentId without file']+=len(agentids - have)
        E['subagent link']['file without agentId']+=len(have - agentids)
        E['subagent meta files'][str(sorted(set(n.split('.')[-1] for n in os.listdir(sub))))]+=1
    for r in rows:
        if r.get('type')=='user' and isinstance((r.get('message') or {}).get('content'),list):
            for b in r['message']['content']:
                if isinstance(b,dict) and b.get('type')=='image':
                    src=b.get('source') or {}
                    E['user image source'][str(src.get('type'))+':'+str(src.get('media_type'))]+=1
for k in E:
    print('==',k); [print(f'   {v:6d}  {x}') for x,v in E[k].most_common(12)]
