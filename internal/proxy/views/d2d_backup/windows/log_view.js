Ext.define('PBS.plusPanel.LogView', {
  extend: 'Ext.panel.Panel',
  xtype: 'proxmoxPlusLogView',

  pageSize: 510,
  viewBuffer: 50,
  lineHeight: 16,

  scrollToEnd: true,

  // maximum number of live log lines to keep cached
  maxCachedLines: 1000,

  // callback for load failure, used for ceph
  failCallback: undefined,

  controller: {
    xclass: 'Ext.app.ViewController',

    updateParams: function() {
      let me = this;
      let viewModel = me.getViewModel();

      if (viewModel.get('hide_timespan') || viewModel.get('livemode')) {
        return;
      }

      let since = viewModel.get('since');
      let until = viewModel.get('until');

      if (since > until) {
        Ext.Msg.alert('Error', 'Since date must be less equal than Until date.');
        return;
      }

      let submitFormat = viewModel.get('submitFormat');

      viewModel.set('params.since', Ext.Date.format(since, submitFormat));
      if (submitFormat === 'Y-m-d') {
        viewModel.set('params.until', Ext.Date.format(until, submitFormat) +
          ' 23:59:59');
      }
      else {
        viewModel.set('params.until', Ext.Date.format(until, submitFormat));
      }

      me.getView().loadTask.delay(200);
    },

    scrollPosBottom: function() {
      let view = this.getView();
      let pos = view.getScrollY();
      let maxPos = view.getScrollable().getMaxPosition().y;
      return maxPos - pos;
    },

    /**
     * updateView builds a virtualized view. It creates spacer divs to simulate
     * the full file height and shows only a “window” of fetched log lines.
     *
     * In live mode (when scrollToEnd is true) we are accumulating log lines in the
     * cachedLines array. We use that cache to update the display; if its length is
     * larger than the configured maximum, we discard old lines.
     */
    updateView: function(lines, startLine, total) {
      let me = this;
      let view = me.getView();
      let viewModel = me.getViewModel();
      let content = me.lookup('content');
      let data = viewModel.get('data');

      // Avoid unnecessary updates (except when first output is coming)
      if (startLine === data.start &&
          total === data.total &&
          lines.length === data.fetchedLines) {
        if (total !== 1) {
          return;
        }
      }
      viewModel.set('data', {
        start: startLine,
        total: total,
        fetchedLines: lines.length,
      });

      let scrollPos = me.scrollPosBottom();
      let scrollToBottom = view.scrollToEnd && scrollPos <= 5;

      // For non-live mode we use virtualization with spacer divs.
      // For live mode we expect to be in scrollToEnd state.
      let aboveHeight = startLine * view.lineHeight;
      let belowCount = total - startLine - lines.length;
      let belowHeight =
        belowCount > 0 ? belowCount * view.lineHeight : 0;

      let spacerTop = `<div style="height:${aboveHeight}px"></div>`;
      let spacerBottom = `<div style="height:${belowHeight}px"></div>`;

      let htmlContent = spacerTop + lines.join('<br>') + spacerBottom;
      content.update(htmlContent);

      if (scrollToBottom) {
        let scroller = view.getScrollable();
        scroller.suspendEvent('scroll');
        view.scrollTo(0, Infinity);
        me.updateStart(true);
        scroller.resumeEvent('scroll');
      }
    },

    /**
     * doLoad initiates an API call. In live mode we accumulate the log lines
     * into a cachedLines array and trim old entries if needed. In non-live mode
     * we simply update the view based on the fetched chunk.
     */
    doLoad: function() {
      let me = this;
      if (me.running) {
        me.requested = true;
        return;
      }
      me.running = true;
      let view = me.getView();
      let viewModel = me.getViewModel();

      Proxmox.Utils.API2Request({
        url: view.url,
        params: viewModel.get('params'),
        method: 'GET',
        success: function(response) {
          if (me.isDestroyed) {
            return;
          }
          Proxmox.Utils.setErrorMask(me, false);

          let total = response.result.total;
          let newFetchedLines = [];
          Ext.Array.each(response.result.data, function(line) {
            newFetchedLines.push(Ext.htmlEncode(line.t));
          });

          // If we are in live mode, accumulate log lines.
          if (viewModel.get('livemode')) {
            if (!me.cachedLines) {
              me.cachedLines = newFetchedLines;
            } else {
              // We assume API returns the tail chunk.
              // Append new lines if the total has increased.
              let currentCount = me.cachedLines.length;
              if (total > currentCount) {
                // Append any new lines (if API chunk overlaps, duplicates
                // will be minimal).
                me.cachedLines = me.cachedLines.concat(newFetchedLines);
              }
            }

            // Trim cachedLines if exceeding maxCachedLines.
            if (me.maxCachedLines && me.cachedLines.length > me.maxCachedLines) {
              let removeCount = me.cachedLines.length - me.maxCachedLines;
              me.cachedLines.splice(0, removeCount);
            }
            // The "start" in the view is now shifted:
            let effectiveStart = total - me.cachedLines.length;
            me.updateView(me.cachedLines, effectiveStart, total);
          }
          else {
            // Non-live mode: simply update using the current chunk.
            let startParam = viewModel.get('params.start') || 0;
            me.updateView(newFetchedLines, startParam, total);
          }

          me.running = false;
          if (me.requested) {
            me.requested = false;
            view.loadTask.delay(200);
          }
        },
        failure: function(response) {
          if (view.failCallback) {
            view.failCallback(response);
          }
          else {
            let msg = response.htmlStatus;
            Proxmox.Utils.setErrorMask(me, msg);
          }
          me.running = false;
          if (me.requested) {
            me.requested = false;
            view.loadTask.delay(200);
          }
        }
      });
    },

    updateStart: function(scrolledToBottom, targetLine) {
      let me = this;
      let view = me.getView(), viewModel = me.getViewModel();

      let limit = viewModel.get('params.limit');
      let total = viewModel.get('data').total || 0;

      // Heuristic: if scrolling up load more content before and vice versa.
      let startRatio = view.lastTargetLine && view.lastTargetLine > targetLine ? 2/3 : 1/3;
      view.lastTargetLine = targetLine;

      let newStart = scrolledToBottom
        ? Math.trunc(total - limit)
        : Math.trunc(targetLine - startRatio * limit + 10);

      viewModel.set('params.start', Math.max(newStart, 0));

      view.loadTask.delay(200);
    },

    onScroll: function(x, y) {
      let me = this;
      let view = me.getView(), viewModel = me.getViewModel();

      let line = view.getScrollY() / view.lineHeight;
      let viewLines = view.getHeight() / view.lineHeight;

      let viewStart = Math.max(Math.trunc(line - 1 - view.viewBuffer), 0);
      let viewEnd = Math.trunc(line + viewLines + 1 + view.viewBuffer);

      let { start, limit } = viewModel.get('params');
      let margin = start < 20 ? 0 : 20;

      if (viewStart < start + margin || viewEnd > start + limit - margin) {
        me.updateStart(false, line);
      }
    },

    onLiveMode: function() {
      let me = this;
      let viewModel = me.getViewModel();
      viewModel.set('livemode', true);
      viewModel.set('params', { start: 0, limit: 510 });
      // Reset the cachedLines so that old log data is cleared.
      me.cachedLines = [];
      let view = me.getView();
      view.scrollToEnd = true;
      me.updateView([], 0, 0);
    },

    onTimespan: function() {
      let me = this;
      me.getViewModel().set('livemode', false);
      // Clear any cached live data.
      me.cachedLines = null;
      me.updateView([], 0, 0);
      me.updateParams();
    },

    init: function(view) {
      let me = this;

      if (!view.url) {
        throw "no url specified";
      }

      let viewModel = this.getViewModel();
      let since = new Date();
      since.setDate(since.getDate() - 3);
      viewModel.set('until', new Date());
      viewModel.set('since', since);
      viewModel.set('params.limit', view.pageSize);
      viewModel.set('hide_timespan', !view.log_select_timespan);
      viewModel.set('submitFormat', view.submitFormat);
      me.lookup('content').setStyle('line-height', `${view.lineHeight}px`);

      view.loadTask = new Ext.util.DelayedTask(me.doLoad, me);

      // Start in live mode by default – cachedLines will accumulate old logs.
      me.cachedLines = null;
      me.updateParams();
      view.task = Ext.TaskManager.start({
        run: () => {
          if (!view.isVisible() || !view.scrollToEnd) {
            return;
          }
          if (me.scrollPosBottom() <= 5) {
            view.loadTask.delay(200);
          }
        },
        interval: 1000,
      });
    }
  },

  onDestroy: function() {
    let me = this;
    me.loadTask.cancel();
    Ext.TaskManager.stop(me.task);
  },

  // Allow external callers to trigger a load.
  requestUpdate: function() {
    let me = this;
    me.loadTask.delay(200);
  },

  viewModel: {
    data: {
      until: null,
      since: null,
      submitFormat: 'Y-m-d',
      livemode: true,
      hide_timespan: false,
      data: {
        start: 0,
        total: 0,
        textlen: 0,
      },
      params: {
        start: 0,
        limit: 510,
      },
    },
  },

  layout: 'auto',
  bodyPadding: 5,
  scrollable: {
    x: 'auto',
    y: 'auto',
    listeners: {
      // We attach to the internal scroller’s scroll event.
      scroll: {
        fn: function(scroller, x, y) {
          let controller = this.component.getController();
          if (controller) {
            controller.onScroll(x, y);
          }
        },
        buffer: 200,
      },
    },
  },

  tbar: {
    bind: { hidden: '{hide_timespan}' },
    items: [
      '->',
      {
        xtype: 'segmentedbutton',
        items: [
          {
            text: gettext('Live Mode'),
            bind: { pressed: '{livemode}' },
            handler: 'onLiveMode',
          },
          {
            text: gettext('Select Timespan'),
            bind: { pressed: '{!livemode}' },
            handler: 'onTimespan',
          },
        ],
      },
      {
        xtype: 'box',
        autoEl: { cn: gettext('Since') + ':' },
        bind: { disabled: '{livemode}' },
      },
      {
        xtype: 'proxmoxDateTimeField',
        name: 'since_date',
        reference: 'since',
        format: 'Y-m-d',
        bind: {
          disabled: '{livemode}',
          value: '{since}',
          maxValue: '{until}',
          submitFormat: '{submitFormat}',
        },
      },
      {
        xtype: 'box',
        autoEl: { cn: gettext('Until') + ':' },
        bind: { disabled: '{livemode}' },
      },
      {
        xtype: 'proxmoxDateTimeField',
        name: 'until_date',
        reference: 'until',
        format: 'Y-m-d',
        bind: {
          disabled: '{livemode}',
          value: '{until}',
          minValue: '{since}',
          submitFormat: '{submitFormat}',
        },
      },
      {
        xtype: 'button',
        text: 'Update',
        handler: 'updateParams',
        bind: { disabled: '{livemode}' },
      },
    ],
  },

  items: [
    {
      xtype: 'box',
      reference: 'content',
      style: {
        font: 'normal 11px tahoma, arial, verdana, sans-serif',
        'white-space': 'pre',
      },
    },
  ],
});
