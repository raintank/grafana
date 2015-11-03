define([
  'angular',
  'app/app',
  'lodash',
  'app/components/panelmeta',
],
function (angular, app, _, PanelMeta) {
  'use strict';

  var module = angular.module('grafana.panels.raintankCollectorList',  []);
  app.useModule(module);

  app.useModule(module);
  module.directive('grafanaPanelRaintankcollectorlist', function() {
    return {
      controller: 'raintankCollectorList',
      templateUrl: 'plugins/raintank/panels/raintankCollectorList/module.html',
    };
  });

  // module.controller('raintankCollectorList', function($scope, $http, $location, $rootScope, $q, backendSrv, panelSrv) {
  module.controller('raintankCollectorList', function($scope, $http, $location, $rootScope, $q, backendSrv) {
    $scope.panelMeta = new PanelMeta({
      panelName: 'Raintank Collector List',
      description : "Collector List",
      fullscreen: false
    });
    $scope.panel.title = "";
    $scope.pageReady = false;
    $scope.statuses = [
      {label: "Online", value: {online: true, enabled: true}, id: 2},
      {label: "Offline", value: {online: false, enabled: true}, id: 3},
      {label: "Disabled", value: {enabled: false}, id: 4},
    ];

    $scope.init = function() {
      $scope.filter = {tag: "", status: ""};
      $scope.sort_field = "name";
      $scope.collectors = [];
      $scope.getCollectors();
    };

    $scope.collectorTags = function() {
      var map = {};
      _.forEach($scope.collectors, function(collector) {
        _.forEach(collector.tags, function(tag) {
          map[tag] = true;
        });
      });
      return Object.keys(map);
    };

    // $scope.setCollectorFilter = function(tag) {
    //   $scope.filter.tag = tag;
    // };
    // $scope.statusFilter = function(actual) {
    //   if (!$scope.filter.status) {
    //     return true;
    //   }
    //   var res = $filter('filter')([actual], $scope.filter.status);
    //   return res.length > 0;
    // };
    $scope.getCollectors = function() {
      backendSrv.get('/api/collectors').then(function(collectors) {
        $scope.pageReady = true;
        $scope.collectors = collectors;
      });
    };

    $scope.remove = function(loc) {
      backendSrv.delete('/api/collectors/' + loc.id).then(function() {
        $scope.getCollectors();
      });
    };

    $scope.gotoDashboard = function(collector) {
      $location.path("/dashboard/file/rt-collector-summary.json").search({"var-collector": collector.slug, "var-endpoint": "All"});
    };

    $scope.init();
  });
});
