# Fifth census: api error turns, denial kinds, error prefixes, restart markers. Prints shapes/prefix patterns only.
import json, os, glob, collections, time, re
root = os.path.expanduser('~/.claude/projects')
cutoff = time.time() - 30*86400
files = [f for f in glob.glob(root+'/*/*.jsonl') if os.path.getmtime(f) > cutoff]
C = collections.Counter
E = collections.defaultdict(C)
def text_of(c):
    if isinstance(c,str): return c
    if isinstance(c,list): return '\n'.join((b.get('text') or '') for b in c if isinstance(b,dict))
    return ''
def shape(s): return re.sub(r'[0-9]+','N',s[:28])
for f in files:
    rows=[]
    for line in open(f,'rb'):
        try: r=json.loads(line)
        except Exception: continue
        if isinstance(r,dict): rows.append(r)
    sc=sum(1 for r in rows if r.get('type')=='attachment' and isinstance(r.get('attachment'),dict) and r['attachment'].get('type') in ('session_context',))
    env=sum(1 for r in rows if r.get('type')=='attachment' and isinstance(r.get('attachment'),dict) and r['attachment'].get('type') in ('environment',))
    E['session_context per main file'][min(sc,5)]+=1
    E['environment per main file'][min(env,5)]+=1
    for i,r in enumerate(rows):
        t=r.get('type'); m=r.get('message') if isinstance(r.get('message'),dict) else {}
        if t=='assistant' and r.get('isApiErrorMessage'):
            nxt=[ (x.get('type')+('/'+str(x.get('subtype')) if x.get('subtype') else '')) for x in rows[i+1:i+4]]
            E['api error: next rows'][' > '.join(nxt)]+=1
            E['api error: model/stop'][str(m.get('model'))+'/'+str(m.get('stop_reason'))+'/err='+str(r.get('error'))]+=1
            E['api error: text shape'][shape(text_of(m.get('content')))]+=1
        if t=='assistant' and r.get('isAbortedMidStream'):
            nxt=[ (x.get('type')+('/'+str(x.get('subtype')) if x.get('subtype') else '')) for x in rows[i+1:i+3]]
            E['abortedMidStream: next'][' > '.join(nxt)]+=1
        if t=='user' and 'toolDenialKind' in r:
            E['denial kind -> content shape'][r['toolDenialKind']+' :: '+shape(text_of([b for b in (m.get('content') or []) if isinstance(b,dict) and b.get('type')=='tool_result'][0].get('content')) if isinstance(m.get('content'),list) and any(isinstance(b,dict) and b.get('type')=='tool_result' for b in m['content']) else '')]+=1
            E['denial kind by version'][r['toolDenialKind']+' '+str(r.get('version'))[:7]]+=1
        if t=='user' and isinstance(m.get('content'),list):
            for b in m['content']:
                if isinstance(b,dict) and b.get('type')=='tool_result' and b.get('is_error') and 'toolDenialKind' not in r:
                    E['is_error w/o denial: content shape'][shape(text_of(b.get('content')) if not isinstance(b.get('content'),str) else b['content'])]+=1
        if t=='system' and r.get('subtype')=='compact_boundary':
            E['compact_boundary neighbors'][' > '.join(x.get('type')+('/'+str(x.get('subtype')) if x.get('subtype') else '') for x in rows[max(0,i-2):i+3])]+=1
            E['compactMetadata keys'][','.join(sorted(r.get('compactMetadata') or {}))]+=1
for k in E:
    print('==',k); [print(f'   {v:6d}  {x}') for x,v in E[k].most_common(14)]
