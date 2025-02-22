Ext.define("pbs-disk-backup-job-status", {
  extend: "Ext.data.Model",
  fields: [
    "id",
    "store",
    "target",
    "subpath",
    "ns",
    "schedule",
    "comment",
    "duration",
    "current_read_total",
    "current_write_total",
    "current_read_speed",
    "current_write_speed",
    "next-run",
    "last-run-upid",
    "last-run-state",
    "last-run-endtime",
    "rawexclusions",
    "retry",
  ],
  idProperty: "id",
  proxy: {
    type: "proxmox",
    url: pbsPlusBaseUrl + "/api2/json/d2d/backup",
  },
});

Ext.define("pbs-model-targets", {
  extend: "Ext.data.Model",
  fields: [
    "name",
    "path",
    "drive_type",
    "agent_version",
    "connection_status",
    "drive_name",
    "drive_fs",
    "drive_total_bytes",
    "drive_used_bytes",
    "drive_free_bytes",
    "drive_total",
    "drive_used",
    "drive_free",
  ],
  idProperty: "name",
});

Ext.define("pbs-model-tokens", {
  extend: "Ext.data.Model",
  fields: ["token", "comment", "created_at", "revoked"],
  idProperty: "token",
});

Ext.define("pbs-model-exclusions", {
  extend: "Ext.data.Model",
  fields: ["path", "comment"],
  idProperty: "path",
});
