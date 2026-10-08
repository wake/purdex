# Second census: enum values of fields relevant to normalization. Prints only enums/key names/counts/lengths.
import json, os, glob, collections, time, re
root = os.path.expanduser('~/.claude/projects')
cutoff = time.time() - 14*86400
files = [f for f in glob.glob(root+'/**/*.jsonl', recursive=True) if os.path.getmtime(f) > cutoff]
C = collections.Counter
E = collections.defaultdict(C)
pathpat=C()
for f in files:
    rel=os.path.relpath(f,root); parts=rel.split('/')
    pathpat['/'.join(['<proj>']+[re.sub(r'[0-9a-f-]{36}','<uuid>',re.sub(r'agent-[0-9a-z]+','agent-<id>',p)) for p in parts[1:]])]+=1
    msgrows=C(); main = '/subagents/' not in f
    try: fh=open(f,'rb')
    except Exception: continue
    for line in fh:
        try: r=json.loads(line)
        except Exception: continue
        if not isinstance(r,dict): continue
        t=r.get('type')
        for k in ['turnOrigin','permissionDecision','toolDenialKind','turnPosition','turnCompanion','queuePriority','sessionKind','perTurnEffort','effort','userType','isAbortedMidStream','isApiErrorMessage','error','apiErrorStatus','queueOrigin']:
            if k in r:
                v=r[k]; E[f'{t}.{k}'][v if isinstance(v,(str,int,bool,type(None))) else ('dict:'+','.join(sorted(v)) if isinstance(v,dict) else type(v).__name__)]+=1
        if t=='user' and 'origin' in r and isinstance(r['origin'],dict): E['user.origin.kind+keys'][r['origin'].get('kind','?')+':'+','.join(sorted(r['origin']))]+=1
        if t=='user' and 'interruptedMessageId' in r: E['user.interruptedMessageId.contentkind'][type((r.get('message') or {}).get('content')).__name__]+=1
        m=r.get('message') if isinstance(r.get('message'),dict) else {}
        if t=='assistant':
            E['assistant.model'][m.get('model')]+=1
            if m.get('id'): msgrows[m['id']]+=1
            E['assistant.apiBlockIndex'][r.get('apiBlockIndex')]+=1
            for b in (m.get('content') or []):
                if isinstance(b,dict) and b.get('type')=='thinking':
                    E['thinking.textlen_bucket']['0' if not b.get('thinking') else ('<100' if len(b['thinking'])<100 else '>=100')]+=1
                    E['thinking.keys'][','.join(sorted(b))]+=1
            E['assistant.content_blocks_per_row'][len(m.get('content') or [])]+=1
        if t=='system':
            st=r.get('subtype')
            E['system.'+str(st)+'.keys'][','.join(sorted(k for k in r if k not in ('parentUuid','isSidechain','type','subtype','timestamp','uuid','userType','entrypoint','cwd','sessionId','version','gitBranch','session_id','slug','sessionKind')))]+=1
            if 'content' in r and isinstance(r['content'],str): E['system.'+str(st)+'.content_prefix'][re.sub(r'[0-9]+','N',r['content'][:40])]+=1
        if t=='attachment':
            a=r.get('attachment') if isinstance(r.get('attachment'),dict) else {}
            at=a.get('type')
            if at in ('queued_command','edited_text_file','hook_additional_context','file','max_turns_reached','thinking_drop','read_truncation_notice','model','remote_session_change','prompt_snapshot','hook_cancelled','auto_mode'):
                E['attachment.'+at+'.keys'][','.join(sorted(a))]+=1
            if at=='queued_command':
                for k in ('commandMode','origin','source_uuid','isMeta'):
                    if k in a: E['attachment.queued_command.'+k][str(a[k])[:50] if not isinstance(a[k],dict) else 'dict:'+','.join(sorted(a[k]))]+=1
        if t=='queue-operation':
            E['queue.op+keys'][r.get('operation')+':'+','.join(sorted(r))]+=1
            if 'reason' in r: E['queue.reason'][r['reason']]+=1
        if t=='user':
            c=m.get('content')
            if isinstance(c,list):
                kinds=tuple(sorted(set(b.get('type') for b in c if isinstance(b,dict))))
                E['user.content_list_kinds'][kinds]+=1
            if r.get('isCompactSummary'): E['user.isCompactSummary'][type(c).__name__]+=1
            if r.get('isMeta'): E['user.isMeta.contentkind'][type(c).__name__]+=1
            if 'toolUseResult' in r and isinstance(c,list):
                E['user.toolresult_blocks_per_row'][sum(1 for b in c if isinstance(b,dict) and b.get('type')=='tool_result')]+=1
    for k,v in msgrows.items(): E['assistant.rows_per_message_id' + ('(main)' if main else '(sub)')][min(v,10)]+=1
print('== path patterns'); [print(f'   {v:7d}  {k}') for k,v in pathpat.most_common(15)]
for name in sorted(E):
    print('==',name)
    for k,v in E[name].most_common(25): print(f'   {v:7d}  {k}')
