javascript:(function() {
  mw.hook("ve.activationComplete").add(function() {
    var target = ve.init.target;
    target.toolbarSaveButton.disconnect(target);

    var context = {};
    context.onToolbarSaveButtonClick = function() {
      target.serialize(target.getDocToSave(), function(wikitext) {
        $.ajax({
          type: "post",
          url: "http://localhost:4000/post",
          data: JSON.stringify({
            host: window.location.host,
            pageName: mw.config.get("wgPageName"),
            articleId: mw.config.get("wgArticleId"),
            curRevisionId: mw.config.get("wgCurRevisionId"),
            wikitext: wikitext,
          }),
          contentType: "application/json",
        })
      });
    };

    target.toolbarSaveButton.connect(context, {
      click: "onToolbarSaveButtonClick",
    });
  });
}());
