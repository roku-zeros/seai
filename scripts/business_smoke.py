#!/usr/bin/env python3
"""CLI acceptance: typed Go rules, shared impact, candidate gates and business cases."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--bin',required=True,type=Path)
a=p.parse_args()
binaries=a.bin.resolve()
source=Path(__file__).resolve().parents[1]
validators={}
if os.environ.get('SEAI_VALIDATE_SCHEMAS')=='1':
    import jsonschema
    from referencing import Registry,Resource
    schemas=[json.loads(path.read_text()) for path in (source/'schemas').glob('*.json')]
    registry=Registry().with_resources((s['$id'],Resource.from_contents(s)) for s in schemas)
    for s in schemas:
        jsonschema.Draft202012Validator.check_schema(s)
        validators[s['$id'].rsplit('/',1)[1]]=jsonschema.Draft202012Validator(s,registry=registry)
def run(args):
    return subprocess.run([str(x) for x in args],check=True,capture_output=True,text=True).stdout
with tempfile.TemporaryDirectory(prefix='seai-business-') as tmp:
    root=Path(tmp)
    repo=root/'repo'
    shutil.copytree(source/'fixtures/business',repo)
    for args in [['init','-q'],['config','user.name','Business fixture'],['config','user.email','fixture@example.invalid'],['add','.'],['commit','-qm','business fixture']]:
        run(['git','-C',repo,*args])
    original=(repo/'rules.go').read_bytes()
    cfg=root/'runtime.json'
    run(['python3',source/'scripts/configure_business_fixture.py','--output',cfg,'--go',shutil.which('go'),'--approve-local-execution'])
    if 'runtime-v1.schema.json' in validators:validators['runtime-v1.schema.json'].validate(json.loads(cfg.read_text()))
    data=Path(tempfile.mkdtemp(prefix='sb-',dir='/tmp'))
    proc=None
    with (root/'daemon.log').open('w+') as log:
        try:
            def start():
                proc=subprocess.Popen([str(binaries/'seaid'),'--repo',str(repo),'--data',str(data),'--project','business-smoke','--config',str(cfg)],stdout=log,stderr=log)
                for _ in range(200):
                    if proc.poll() is not None:
                        log.seek(0)
                        raise RuntimeError(log.read())
                    if (data/'seaid.sock').exists():return proc
                    time.sleep(.05)
                proc.terminate();proc.wait(timeout=20)
                raise RuntimeError('startup timeout')
            def cli(*args):
                value=json.loads(run([binaries/'seai','--data',data,*args]))
                assert value.get('error') is None,value
                name=None
                if args[0]=='search':name='search-result-v1.schema.json'
                elif args[0]=='rules' and args[1]=='search':name='rule-list-v1.schema.json'
                elif args[0] in ['impact','flow']:name='impact-v1.schema.json'
                if name in validators:validators[name].validate(value['data'])
                return value['data']
            def wait(accepted):
                rid=accepted['run_id']
                for _ in range(3000):
                    result=cli('run','status',rid)
                    if result['state'] not in ['RUNNING','QUEUED']:return result
                    time.sleep(.1)
                raise RuntimeError('execution timeout')
            proc=start()
            assert cli('index','build','--wait')['state']=='SUCCEEDED'
            assert cli('rules','discover')['candidates']
            found=cli('rules','search','AGE_MIN')
            assert len(found['rules'])==2,found
            assert len(cli('search','AGE_MIN')['business_rules'])==2
            ambiguous=subprocess.run([str(binaries/'seai'),'--data',str(data),'rules','show','AGE_MIN'],capture_output=True,text=True)
            assert ambiguous.returncode==3 and json.loads(ambiguous.stdout)['error']['code']=='AMBIGUOUS_RULE'
            detail=cli('rules','show','AGE_MIN','--context','Product=retail')
            assert detail['rule']['declared_priority']=='10' and detail['source_facts']
            impact=cli('impact','MinimumAge','--depth','5','--limit','100','--context','Product=retail')
            assert {'Product=retail','Product=business'}<=set(impact['affected_contexts']),impact
            assert impact['related_tests'] and impact['possible']
            flow=cli('flow',detail['rule']['entity_id'],'--depth','5','--limit','100')
            assert any(n['name']=='MinimumAge' for n in flow['nodes'])
            profile=next(x for x in cli('checks','profiles') if x['id']=='business')
            spec=dict(schema_version='seai.change/v1',change_id='BUSINESS-SMOKE',revision=1,kind='refactor',
                objective='Rename helper without changing decisions, exceptions or priority',source_requirements=[],scope=dict(files=['rules.go']),
                expected_behavior=[],preserved_behavior=[dict(id='INV',statement='All product decisions and reasons preserved')],
                acceptance_criteria=[dict(id='AC1',behavior_ids=['INV'],expected='All approved business scenarios and traces equivalent',
                    assertion_profile='business-equivalence-v1',fixture_digest=profile['fixture_digest'])],unresolved=[])
            spec_path=root/'spec.json';spec_path.write_text(json.dumps(spec))
            task=cli('task','create','--spec',str(spec_path))
            plan=dict(schema_version='seai.plan/v1',steps=[dict(id='S1',description='Rename helper and its call',files=['rules.go'],criterion_ids=['AC1'],evidence_ids=[detail['rule']['evidence']['evidence_id']])],required_checks=['compile','tests','business'])
            plan_path=root/'plan.json';plan_path.write_text(json.dumps(plan))
            task=cli('task','plan',task['task_id'],'--plan',str(plan_path))
            old=cli('read','rules.go','--snapshot',task['candidate_snapshot_id'],'--limit','65536')
            content=old['text'].replace('AgeThreshold','MinimumAgeThreshold')
            patch=dict(schema_version='seai.patch/v1',base_snapshot_id=task['candidate_snapshot_id'],spec_digest=task['spec_digest'],files=[dict(path='rules.go',before_digest=old['blob_digest'],before_mode=old['mode'],content=content,after_digest=hashlib.sha256(content.encode()).hexdigest(),mode=old['mode'])])
            patch_path=root/'patch.json';patch_path.write_text(json.dumps(patch))
            task=cli('patch','apply',task['task_id'],'--patch',str(patch_path))
            result=wait(cli('task','verify',task['task_id']))
            assert result['state']=='SUCCEEDED',result
            report=next(x for x in result['result'] if x['profile_id']=='business')
            assert report['status']=='PASSED' and len(report['business_cases'])==9,report
            assert report['business_coverage'][0]['missing_categories']==[] and report['business_coverage'][0]['trace_verified']
            domain=next(x for x in result['result'] if x['profile_id']=='domain-analysis')['domain_verification']
            assert len(domain['affected_rule_ids'])==4 and not domain['coverage_gaps'],domain
            candidate_detail=cli('rules','show','AGE_MIN','--context','Product=retail','--generation',domain['index_generation_id'])
            assert candidate_detail['source_snapshot_id']==task['candidate_snapshot_id']
            state=cli('task','status',task['task_id'])
            assert state['check_summary']['checks_passed'] is True and state['release_readiness']=='NOT_REVIEW_READY',state
            assert (repo/'rules.go').read_bytes()==original
            proc.terminate();proc.wait(timeout=20)
            proc=start()
            assert cli('rules','search','AGE_MIN')['rules']
            assert cli('run','status',result['run_id'])['state']=='SUCCEEDED'
            print(json.dumps(dict(status='passed',version='0.4.0-dev',checks=['typed Go registrations','duplicate IDs by context','source evidence','shared impact across products','forward constants/conditions','candidate domain gate','9 executed business fixtures','normal/boundary/exception/combination coverage','rule trace','candidate generation pinning','restart','source preserved']),indent=2))
        finally:
            if proc is not None and proc.poll() is None:proc.terminate();proc.wait(timeout=20)
            shutil.rmtree(data)
