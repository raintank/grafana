module.exports = function(config) {
  return {
    build: {
      options:{
        removeComments: true,
        collapseWhitespace: true,
        keepClosingSlash: true
      },
      expand: true,
      cwd: '<%= genDir %>',
      src: [
        'plugins/raintank/panels/**/*.html',
        'plugins/raintank/features/**/*.html',
        'plugins/raintank/directives/**/*.html',
        'app/**/*.html'
      ],
      dest: '<%= genDir %>'
    }
  };
};
