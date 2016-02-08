define([
  'angular',
  'app/app',
  'lodash',
  'app/core/utils/kbn',
  'app/features/panel/panel_meta',
],
function (angular, app, _, kbn, PanelMeta) {
  'use strict';
  
  function eventsPanelCtrl($scope, panelSrv, backendSrv, templateSrv, panelHelper) {
    $scope.panelMeta = new PanelMeta({
      panelName: 'Raintank Events',
      description : "Events",
      editIcon:  "fa fa-dashboard",
      fullscreen: true
    });
    $scope.panelMeta.addEditorTab('Filter', 'public/plugins/raintankevents/editor.html');
    $scope.panelMeta.addEditorTab('Time range', 'app/features/panel/partials/panelTime.html');

    // Set and populate defaults
    var _d = {
      filter: null,
      title: "Events",
      size: 10
    };

    _.defaults($scope.panel, _d);

    $scope.init = function() {
      panelSrv.init(this);
    };

    $scope.refreshData = function() {
      panelHelper.updateTimeRange($scope);
      if (!$scope.panel.filter) {
        return;
      }
      if ($scope.panel.filter.indexOf(":", $scope.panel.filter.length - 1) !== -1) {
        //filter ends with a colon. elasticsearch will send a 500error for this.
        return;
      }

      var params = {
        query: templateSrv.replace($scope.panel.filter, $scope.panel.scopedVars),
        start: $scope.range.from.valueOf(),
        end:  $scope.range.to.valueOf(),
        size: $scope.panel.size,
      };

      backendSrv.get('/api/events', params).then(function(events) {
        $scope.events = events;
        if (!('scopedVars' in $scope.panel)) {
          $scope.panel.scopedVars = {};
        }
        $scope.panel.scopedVars['eventCount'] = {selected: true, text: events.length, value: events.length};
      });
    };

    $scope.tagValueByName = function(event, tag) {
      var value = "";
      event.tags.forEach(function(t) {
        var parts = t.split(":", 2);
        if (parts.length !== 2) {
          return;
        }
        if (parts[0] === tag) {
          if (value === "") {
            value = parts[1];
          } else {
            value = value + " " + parts[1];
          }
        }
      });
      return value;
    };

    $scope.init();
  }

  function eventsPanel() {
    return {
      controller: eventsPanelCtrl,
      templateUrl: 'public/plugins/raintankevents/module.html',
    };
  }

  return {
    panel: eventsPanel
  };
});
