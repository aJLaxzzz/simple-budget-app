-- One-time data conversion. Keep the legacy fields and constraints for old backups.
-- No project, task, occurrence, schedule, exception or incident is rewritten.
INSERT INTO history_events(id,series_id,actor,action,payload_json,operation_id)
 SELECT lower(hex(randomblob(16))),series_id,'migration','project_mode_changed',
 json_object('project_mode',json_object('before',mode,'after','independent'),'current_project_id',json_object('before',current_project_id,'after',NULL),'needs_decision',json_object('before',needs_decision,'after',0)),
 '015-independent-projects'
 FROM project_repeat_state WHERE mode='sequential';
UPDATE series SET revision=revision+1,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
 WHERE id IN (SELECT series_id FROM project_repeat_state WHERE mode='sequential');
UPDATE project_repeat_state SET mode='independent',current_project_id=NULL,needs_decision=0 WHERE mode='sequential';
