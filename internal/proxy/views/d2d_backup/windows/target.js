Ext.define('PBS.D2DManagement.TargetEditWindow', {
    extend: 'Proxmox.window.Edit',
    alias: 'widget.pbsTargetEditWindow',
    mixins: ['Proxmox.Mixin.CBind'],

    isCreate: true,
    isAdd: true,
    subject: 'D2D Backup Target',
    cbindData: function(initialConfig) {
	let me = this;

	let driveid = initialConfig.driveid;
	let baseurl = '/api2/extjs/config/d2d-target';

	me.isCreate = !driveid;
	me.url = driveid ? `${baseurl}/${encodeURIComponent(driveid)}` : baseurl;
	me.method = driveid ? 'PUT' : 'POST';

	return { };
    },

    items: [
	{
	    fieldLabel: gettext('Name'),
	    name: 'name',
	    xtype: 'pmxDisplayEditField',
	    renderer: Ext.htmlEncode,
	    allowBlank: false,
	    cbind: {
		editable: '{isCreate}',
	    },
	},
	{
	    fieldLabel: gettext('Path'),
	    name: 'path',
	    xtype: 'pmxDisplayEditField',
	    renderer: Ext.htmlEncode,
	    allowBlank: false,
	    cbind: {
		editable: '{isCreate}',
	    },
	},
    ],
});
