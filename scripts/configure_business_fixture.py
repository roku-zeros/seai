#!/usr/bin/env python3
"""Create profiles and Go adapters for fixtures/business; never overwrites files."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--output',required=True,type=Path)
p.add_argument('--go',default=shutil.which('go'))
p.add_argument('--approve-local-execution',required=True,action='store_true')
a=p.parse_args()
if not a.go or not Path(a.go).is_file():p.error('Go executable required')
common=dict(program=str(Path(a.go).resolve()),isolation='local-approved',acknowledge_local_execution=True,
            timeout_seconds=120,max_output_bytes=262144,memory_mb=0,cpus=0)
profiles=[dict(common,id='compile',kind='compile',args=['build','./...']),
          dict(common,id='tests',kind='tests',args=['test','-json','-count=1','./...'],
               test_names=['TestBusinessBoundaries','TestBusinessExceptionAndPriority'])]
fixtures=[]
# Expected values are fixed by this synthetic fixture contract, not learned from current program output.
for name,category,age,income,vip,allowed,reason,limit,applied in [
 ('ordinary','normal',25,3000,False,True,'eligible',6000,['CREDIT_LIMIT']),
 ('age-below','boundary',17,3000,False,False,'below_minimum_age',0,['AGE_MIN','CREDIT_LIMIT']),
 ('age-at','boundary',18,3000,False,True,'eligible',6000,['CREDIT_LIMIT']),
 ('age-above','boundary',19,3000,False,True,'eligible',6000,['CREDIT_LIMIT']),
 ('vip-exception','exception',17,3000,True,True,'eligible',6000,['CREDIT_LIMIT']),
 ('two-failures','combination',17,1000,False,False,'below_minimum_age',0,['AGE_MIN','INCOME_MIN','CREDIT_LIMIT'])]:
    fixtures.append(dict(id=name,category=category,description=name,rule_keys=['AGE_MIN','INCOME_MIN','CREDIT_LIMIT'],context={'Product':'retail'},
        input=json.dumps(dict(age=age,income=income,vip=vip,product='retail')),
        expected=dict(allowed=allowed,reason=reason,limit=limit,checked_rules=['AGE_MIN','INCOME_MIN','CREDIT_LIMIT'],applied_rules=applied),
        criterion_ids=['AC1'],allowed_paths=[]))
for age,category in [(17,'boundary'),(18,'boundary'),(25,'normal')]:
    allowed=age>=18
    fixtures.append(dict(id=f'business-{age}',category=category,description='Business product has no VIP exception',rule_keys=['AGE_MIN'],context={'Product':'business'},
        input=json.dumps(dict(age=age,income=3000,vip=True,product='business')),
        expected=dict(allowed=allowed,reason='eligible' if allowed else 'below_minimum_age',limit=0,checked_rules=['AGE_MIN'],applied_rules=[] if allowed else ['AGE_MIN']),criterion_ids=['AC1'],allowed_paths=[]))
profiles.append(dict(common,id='business',kind='comparison',args=['run','./cmd/probe'],
    assertion_profile='business-equivalence-v1',fixtures=fixtures,rule_trace_path='/checked_rules',
    required_cases=[dict(rule_key='AGE_MIN',context={'Product':'retail'},categories=['normal','boundary','exception','combination'])]))
content='Retail minimum age is 18 inclusive; VIP bypasses age, but not income. The first rejection reason wins.'
config=dict(schema_version='seai.runtime/v1',checks=profiles,max_workers=2,max_node_attempts=1,max_run_seconds=1800,
    business=dict(schema_version='seai.business/v1',require_for_changes=True,
        adapters=[dict(name='rules',kind='struct',package='seai.fixture/business',symbol='Rule',id_field='ID',
          handler_fields=dict(condition='When',exception='Except',action='Apply'),context_fields=['Product'],priority_field='Priority')],
        requirements=[dict(rule_key='AGE_MIN',context={'Product':'retail'},external_id='REQ-AGE-1',revision='1',
          content=content,content_digest=hashlib.sha256(content.encode()).hexdigest())]))
with a.output.open('x',encoding='utf-8') as f:json.dump(config,f,ensure_ascii=False,indent=2);f.write('\n')
print(a.output.resolve())
