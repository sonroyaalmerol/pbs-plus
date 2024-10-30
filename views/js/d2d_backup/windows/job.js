Ext.define('PBS.D2DManagement.BackupJobEdit', {
    extend: 'Proxmox.window.Edit',
    alias: 'widget.pbsDiskBackupJobEdit',
    mixins: ['Proxmox.Mixin.CBind'],

    userid: undefined,

    isAdd: true,

    subject: gettext('D2D Backup Job'),

    fieldDefaults: { labelWidth: 120 },

    bodyPadding: 0,

    cbindData: function(initialConfig) {
	let me = this;

	let baseurl = '/api2/extjs/config/disk-backup-job';
	let id = initialConfig.id;

	me.isCreate = !id;
	me.url = id ? `${baseurl}/${id}` : baseurl;
	me.method = id ? 'PUT' : 'POST';
	me.autoLoad = !!id;
	me.scheduleValue = id ? null : 'daily';
	me.authid = id ? null : Proxmox.UserName;
	me.editDatastore = me.datastore === undefined && me.isCreate;
	return { };
    },

    viewModel: {
	data: {
	    notificationMode: '__default__',
	},
	formulas: {
	    notificationSystemSelected: (get) => get('notificationMode') === 'notification-system',
	},
    },

    initComponent: function() {
	let me = this;
	// Automatically select the new system for new jobs
	let mode = me.isCreate ? "notification-system" : "__default__";
	me.getViewModel().set('notificationMode', mode);
	me.callParent();
    },

    items: {
	xtype: 'tabpanel',
	bodyPadding: 10,
	border: 0,
	items: [
	    {
		title: gettext('Options'),
		xtype: 'inputpanel',
		onGetValues: function(values) {
		    let me = this;

		    PBS.Utils.delete_if_default(values, 'notify-user');

		    if (me.isCreate) {
			delete values.delete;
		    }

		    return values;
		},
		cbind: {
		    isCreate: '{isCreate}', // pass it through
		},
		column1: [
		    {
			xtype: 'pmxDisplayEditField',
			name: 'id',
			fieldLabel: gettext('Job ID'),
			renderer: Ext.htmlEncode,
			allowBlank: false,
			cbind: {
			    editable: '{isCreate}',
			},
		    },
		    {
			xtype: 'pbsD2DTargetSelector',
			fieldLabel: 'Target',
			name: 'target',
		    },
		    {
			xtype: 'pbsDataStoreSelector',
			fieldLabel: gettext('Local Datastore'),
			name: 'store',
		    },
		    {
			xtype: 'proxmoxKVComboBox',
			comboItems: [
			    ['__default__', `${Proxmox.Utils.defaultText}  (Email)`],
			    ['legacy-sendmail', gettext('Email (legacy)')],
			    ['notification-system', gettext('Notification system')],
			],
			fieldLabel: gettext('Notification mode'),
			name: 'notification-mode',
			bind: {
			    value: '{notificationMode}',
			},
		    },
		    {
			xtype: 'pmxUserSelector',
			name: 'notify-user',
			fieldLabel: gettext('Notify User'),
			emptyText: 'root@pam',
			allowBlank: true,
			value: null,
			renderer: Ext.String.htmlEncode,
			bind: {
			    disabled: "{notificationSystemSelected}",
			},
		    },
		],

		column2: [
		    {
			fieldLabel: gettext('Schedule'),
			xtype: 'pbsCalendarEvent',
			name: 'schedule',
			emptyText: gettext('none (disabled)'),
			cbind: {
			    deleteEmpty: '{!isCreate}',
			    value: '{scheduleValue}',
			},
		    },
		],

		columnB: [
		    {
			fieldLabel: gettext('Comment'),
			xtype: 'proxmoxtextfield',
			name: 'comment',
			cbind: {
			    deleteEmpty: '{!isCreate}',
			},
		    },
		],
	    },
	],
    },
});

