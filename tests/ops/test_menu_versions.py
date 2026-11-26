import contextlib
import io
import os
from pathlib import Path
import sys
import unittest
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
import version_status as vs
import server_manager as menu
import terminal_ui as ui

class MenuVersions(unittest.TestCase):
    def test_exact_objects_moscow_ancestry_and_local_origin_without_network(self):
        head,server,missing,origin=['a'*40,'b'*40,'c'*40,'d'*40]
        def git(root,*args):
            if args[0]=='rev-parse':return (head if args[-1]=='HEAD' else origin)+'\n'
            if args[0]=='show':
                sha=args[3].split('^')[0]
                return None if sha==missing else sha+'\nTitle '+sha[0]+'\n2026-11-26T09:01:02Z\n'
            if args[0]=='rev-list':return '2 3' if server in args else '1 0'
            self.fail('Unexpected Git/network operation '+str(args))
        # rev-list receives one range string, so recognize the server within it.
        base=git
        def read(root,*args):
            if args[0]=='rev-list':return '2 3' if args[-2].endswith(server) else '1 0'
            return base(root,*args)
        with patch.object(vs,'git',side_effect=read):data=vs.menu_snapshot('/synthetic',{'production':{'revision':server},'acceptance':{'revision':missing}})
        self.assertEqual(data['production']['relation'],'История разошлась')
        self.assertEqual(data['origin']['relation'],'Отстаёт от HEAD')
        self.assertIsNone(data['acceptance']['commit']);self.assertEqual(data['acceptance']['revision'],missing)
        self.assertEqual(vs.menu_metadata(data['production']),['Title b','26.11.2026 12:01:02 МСК'])
        for width,height in [(60,20),(80,24),(100,30),(160,50)]:
            with patch.object(ui,'terminal_size',return_value=(width,height)),patch.dict(os.environ,{'OUTPUT':'json'}):
                lines=menu.menu_lines('acceptance','synthetic',{'production':{'revision':server,'service':'running'},'acceptance':{'revision':missing}},'manual','session','dirty',versions=data)
            text='\n'.join(lines)
            self.assertNotIn('\033',text);self.assertNotIn('Title c',text)
            self.assertIn('локально известный',text);self.assertIn('GitHub',text)
            self.assertTrue(all(ui.visible_width(line)<width for line in lines))

    def test_all_ancestry_labels_are_based_on_counts(self):
        head,server='a'*40,'b'*40
        for counts,label in [('1 0','Отстаёт от HEAD'),('0 1','Опережает HEAD'),('1 1','История разошлась'),(None,'Не удалось определить')]:
            def git(root,*args):
                if args[0]=='rev-parse':return head if args[-1]=='HEAD' else None
                if args[0]=='rev-list':return counts
                return None
            with self.subTest(counts=counts),patch.object(vs,'git',side_effect=git),patch.object(vs,'commit',side_effect=lambda root,sha:{'revision':sha}):
                data=vs.menu_snapshot('/synthetic',{'production':{'revision':server},'acceptance':{'revision':head}})
            self.assertEqual(data['production']['relation'],label)
            self.assertEqual(data['acceptance']['relation'],'Совпадает с HEAD')

    def test_operation_scopes_and_json_safe_output(self):
        with patch.dict(os.environ,{'OUTPUT':'json'}),patch.object(ui,'terminal_size',return_value=(100,30)):
            self.assertIn('Источник: STG → Назначение: PROD','\n'.join(menu.operation_lines('16','acceptance',{})))
            self.assertIn('Целевое окружение: STG','\n'.join(menu.operation_lines('14','production',{})))
            self.assertIn('локальные настройки','\n'.join(menu.operation_lines('11','production',{})))
            for number in range(1,17):
                result='\n'.join(menu.operation_lines(str(number),'acceptance',{}))
                self.assertIn(str(number).zfill(2)+' — ',result);self.assertNotIn('\033',result)

if __name__=='__main__':unittest.main()
