Ext.define('pbs-disk-backup-job-status', {
    extend: 'Ext.data.Model',
    fields: [
	'id', 'store', 'target', 'schedule', 'comment', 'duration',
	'next-run', 'last-run-upid', 'last-run-state', 'last-run-endtime', 'exclusions',
    ],
    idProperty: 'id',
    proxy: {
	type: 'proxmox',
	url: '/api2/json/d2d/backup',
    },
});

Ext.define('pbs-model-targets', {
    extend: 'Ext.data.Model',
    fields: [
	'name', 'path',
    ],
    idProperty: 'name',
});

Ext.define('pbs-model-exclusions', {
    extend: 'Ext.data.Model',
    fields: [
	'path', 'comment', 'is_global',
    ],
    idProperty: 'path',
});

Ext.define('pbs-model-partial-files', {
    extend: 'Ext.data.Model',
    fields: [
      'substring', 'comment',
    ],
    idProperty: 'substring',
});
