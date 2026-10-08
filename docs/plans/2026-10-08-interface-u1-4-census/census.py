# Read-only census of CC transcript JSONL structure. Prints only key names, enum values and counts, never content.
import json, os, glob, collections, time
root = os.path.expanduser('~/.claude/projects')
cutoff = time.time() - 14*86400  # last 14 days
files = [f for f in glob.glob(root+'/**/*.jsonl', recursive=True) if os.path.getmtime(f) > cutoff]
C = collections.Counter
rowtype=C(); keys=collections.defaultdict(C); vers=C(); utypes=C(); atypes=C(); sysub=C(); attach=C(); qop=C()
psrc=C(); tools=C(); trkeys=C(); trcontent=C(); imgs=0; maxrow=0; bigrows=0; files_n=0; rows=0
markers=C(); meta=C(); side=C(); origin=C(); entrypoint=C(); stopreason=C(); usagekeys=C(); tur=C(); permmode=C()
userstr=C(); toolres_err=C(); subagentfiles=0; levels=C(); imgbytes=0
MK=['[Request interrupted by user for tool use]','[Request interrupted by user]',"doesn't want to proceed",'<command-name>','<local-command-stdout>','<local-command-caveat>','<bash-input>','<bash-stdout>','<task-notification>','<system-reminder>','<pasted_content','This session is being continued from a previous conversation','<cross-session-message','<teammate-message','[Image #']
for f in files:
    files_n+=1
    if '/subagents/' in f: subagentfiles+=1
    try: fh=open(f,'rb')
    except Exception: continue
    for line in fh:
        rows+=1; n=len(line); maxrow=max(maxrow,n)
        if n>1_000_000: bigrows+=1
        try: r=json.loads(line)
        except Exception: rowtype['<bad>']+=1; continue
        if not isinstance(r,dict): rowtype['<nondict>']+=1; continue
        t=r.get('type'); rowtype[t]+=1
        for k in r: keys[t][k]+=1
        if 'version' in r: vers[r['version']]+=1
        if t=='system': sysub[r.get('subtype')]+=1; levels[r.get('level')]+=1
        if t=='attachment':
            a=r.get('attachment') or {}; attach[a.get('type') if isinstance(a,dict) else '?']+=1
        if t=='queue-operation': qop[r.get('operation')]+=1
        if 'promptSource' in r: psrc[(t,r.get('promptSource'))]+=1
        if r.get('isMeta'): meta[t]+=1
        if r.get('isSidechain'): side[t]+=1
        if 'entrypoint' in r: entrypoint[r['entrypoint']]+=1
        if 'permissionMode' in r: permmode[r['permissionMode']]+=1
        if 'origin' in r:
            o=r['origin']; origin[o if isinstance(o,str) else json.dumps(o)[:60]]+=1
        if 'toolUseResult' in r:
            tu=r['toolUseResult']
            tur[(type(tu).__name__ + ':' + ','.join(sorted(tu.keys())[:8])) if isinstance(tu,dict) else type(tu).__name__]+=1
        m=r.get('message') or {}
        if not isinstance(m,dict): continue
        if t=='assistant':
            stopreason[m.get('stop_reason')]+=1
            for k in (m.get('usage') or {}): usagekeys[k]+=1
        c=m.get('content')
        if isinstance(c,str):
            userstr[t]+=1
            for mk in MK:
                if mk in c: markers[(t,'str',mk)]+=1
        elif isinstance(c,list):
            for b in c:
                if not isinstance(b,dict): continue
                bt=b.get('type'); (utypes if t=='user' else atypes)[bt]+=1
                if bt=='tool_use': tools[b.get('name')]+=1
                if bt=='text':
                    for mk in MK:
                        if mk in (b.get('text') or ''): markers[(t,'text',mk)]+=1
                if bt=='tool_result':
                    trkeys[','.join(sorted(b.keys()))]+=1
                    cc=b.get('content')
                    if isinstance(cc,list):
                        for x in cc:
                            if isinstance(x,dict):
                                trcontent['list:'+str(x.get('type'))]+=1
                                if x.get('type')=='image':
                                    imgs+=1; imgbytes+=len(json.dumps(x))
                    else: trcontent[type(cc).__name__]+=1
                    if b.get('is_error'):
                        s=json.dumps(cc)[:400]
                        toolres_err['denied' if "doesn't want to proceed" in s else ('interrupted' if 'Request interrupted' in s else 'other')]+=1
def show(name,c,n=40):
    print(f'== {name}')
    for k,v in c.most_common(n): print(f'   {v:7d}  {k}')
print('files',files_n,'subagent files',subagentfiles,'rows',rows,'maxrow',maxrow,'rows>1MB',bigrows,'tool_result images',imgs,'image bytes',imgbytes)
show('row type',rowtype); show('versions',vers,15)
for t,kc in keys.items(): print(f'== keys[{t}]', ' '.join(f'{k}:{v}' for k,v in kc.most_common()))
show('system subtype',sysub); show('system level',levels); show('attachment type',attach,60); show('queue op',qop); show('promptSource',psrc)
show('isMeta by type',meta); show('isSidechain by type',side); show('entrypoint',entrypoint); show('permissionMode',permmode); show('origin',origin,15)
show('user content block types',utypes); show('assistant content block types',atypes); show('string content by type',userstr)
show('tools',tools,80); show('tool_result keys',trkeys); show('tool_result content',trcontent); show('tool_result is_error',toolres_err)
show('toolUseResult shapes',tur,40); show('stop_reason',stopreason); show('usage keys',usagekeys); show('markers',markers,60)
