import importlib.util
from pathlib import Path
import unittest

spec=importlib.util.spec_from_file_location('smoke_support',Path(__file__).resolve().parents[1]/'smoke_support.py')
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class AsyncWaitTests(unittest.TestCase):
    def test_success_after_sixty_seconds_is_not_failed(self):
        elapsed=[0.0]
        def fetch(run_id):
            self.assertEqual(run_id,'slow-run')
            elapsed[0]+=25
            return dict(state='SUCCEEDED' if elapsed[0]>=75 else 'RUNNING')
        result=module.wait_for_run(fetch,'slow-run',120,clock=lambda:elapsed[0],sleep=lambda n:None)
        self.assertEqual(result['state'],'SUCCEEDED')
    def test_timeout_names_run_and_does_not_claim_check_failure(self):
        elapsed=[0.0]
        def sleep(n):elapsed[0]+=n
        with self.assertRaisesRegex(TimeoutError,'run slow-run'):
            module.wait_for_run(lambda rid:dict(state='RUNNING'),'slow-run',.5,clock=lambda:elapsed[0],sleep=sleep)
    def test_terminal_failure_is_not_success(self):
        for state in ['FAILED','CANCELLED','ABORTED','NEEDS_INPUT']:
            with self.subTest(state=state),self.assertRaisesRegex(RuntimeError,state):
                module.wait_for_run(lambda rid:dict(state=state),'failed-run',120)
    def test_invalid_wait_budget(self):
        for value in [0,-1,float('inf'),float('nan')]:
            with self.subTest(value=value),self.assertRaises(ValueError):
                module.wait_for_run(lambda rid:dict(state='SUCCEEDED'),'run',value)
