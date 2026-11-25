import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
import mac
import version_status as vs

A='a'*40
B='b'*40
DATE='2026-11-26T00:00:00+03:00'


class VersionStatusTests(unittest.TestCase):
    def fake_git(self,root,*args):
        if args[0]=='rev-parse':return A+'\n'
        if args[0]=='status':return self.status
        if args[0]=='show':
            sha=args[3].split('^')[0]
            return sha+'\nSynthetic commit\n'+DATE+'\n' if sha in self.known else None
        if args[0]=='rev-list':return self.counts
        self.fail('Unexpected Git operation')

    def setUp(self):
        self.known={A,B};self.status='';self.counts='1\t0\n'
        self.p=patch.object(vs,'git',side_effect=self.fake_git);self.p.start();self.addCleanup(self.p.stop)

    def test_same_commit_with_dirty_tree_is_not_reported_as_clean(self):
        self.status=' M synthetic.py\n?? untracked.txt\n'
        info=vs.inspect('/unused',{'revision':A,'modified':False})
        self.assertEqual(info['relation'],'same_commit');self.assertTrue(info['local_dirty'])
        self.assertNotIn('synthetic.py',json.dumps(info))
        with contextlib.redirect_stdout(io.StringIO()) as out:vs.display(info,{'revision':A,'modified':False})
        self.assertIn('Незакоммиченные изменения: есть',out.getvalue())
        self.assertIn('незакоммиченные изменения в него не входят',out.getvalue())

    def test_ancestry_comparison_not_timestamp_guess(self):
        for counts,expected in [('1 0','local_ahead'),('0 2','server_ahead'),('2 3','diverged'),(None,'unknown')]:
            self.counts=counts
            self.assertEqual(vs.inspect('/unused',{'revision':B})['relation'],expected)

    def test_unknown_server_object_never_uses_untrusted_manifest_title(self):
        self.known={A}
        info=vs.inspect('/unused',{'revision':B,'title':'invented','date':'tomorrow'})
        self.assertIsNone(info['server_commit']);self.assertEqual(info['relation'],'unknown')
        self.assertEqual(info['local_head'],A)

    def test_missing_git_or_status_is_unknown_not_clean(self):
        with patch.object(vs,'git',return_value=None):info=vs.inspect('/unused',{})
        self.assertIsNone(info['local_head']);self.assertIsNone(info['local_dirty'])
        with contextlib.redirect_stdout(io.StringIO()) as out:vs.display(info,{})
        self.assertIn('недоступны',out.getvalue())

    def test_exact_object_only_and_terminal_subject_sanitized(self):
        with patch.object(vs,'git',return_value=B+'\nWrong\n'+DATE) as git:
            self.assertIsNone(vs.commit('/unused',A))
            self.assertIsNone(vs.commit('/unused','--all'))
            self.assertEqual(git.call_count,1)
        with patch.object(vs,'git',return_value=A+'\nSynthetic\x1b\x00 title\n'+DATE):
            info=vs.commit('/unused',A)
            self.assertEqual(info['title'],'Synthetic title')
            self.assertEqual(info['committed_at'],DATE)

    def test_detailed_status_keeps_full_shas_without_mutating_response(self):
        result={'release':{'revision':B,'modified':False,'schema_version':14}}
        with patch.dict(os.environ,{'OUTPUT':'json'}),contextlib.redirect_stdout(io.StringIO()) as out:
            mac.show_result('status',result,'production')
        data=json.loads(out.getvalue())
        self.assertEqual(data['release']['revision'],B)
        self.assertEqual(data['version_comparison']['local_head'],A)
        self.assertNotIn('version_comparison',result)

    def test_short_status_contains_metadata_target_and_local_state(self):
        with patch.dict(os.environ,{'OUTPUT':''}),contextlib.redirect_stdout(io.StringIO()) as out:
            mac.show_result('status',{'release':{'revision':B,'modified':False,'schema_version':14}},'production')
        text=out.getvalue()
        for value in ('production',B[:12],A[:12],'Synthetic commit',DATE,'Незакоммиченные изменения: нет'):
            self.assertIn(value,text)


class GitReadTests(unittest.TestCase):
    def test_no_fetch_replacements_optional_locks_or_secret_output(self):
        with patch.object(vs.subprocess,'run',return_value=subprocess.CompletedProcess([],0,'result','')) as run:
            self.assertEqual(vs.git('/synthetic','status','--porcelain=v1'),'result')
        args=run.call_args.args[0]
        self.assertIn('--no-replace-objects',args);self.assertNotIn('fetch',args)
        self.assertEqual(run.call_args.kwargs['env']['GIT_OPTIONAL_LOCKS'],'0')
        self.assertEqual(run.call_args.kwargs['timeout'],5)

    def test_failure_or_timeout_does_not_break_server_status(self):
        for error in (FileNotFoundError(),subprocess.TimeoutExpired('git',5)):
            with patch.object(vs.subprocess,'run',side_effect=error):
                self.assertIsNone(vs.git('/synthetic','status'))


if __name__=='__main__':unittest.main()
