(function() {
    mw.libs.ve.targetLoader.addPlugin(function() {
        var torget = function(config) {
            torget.super.call(this, config);
        };
        OO.inheritClass(torget, ve.init.mw.DesktopArticleTarget);
        torget.prototype.save = function(doc, options) {
            var target = ve.init.target;
            target.serialize(doc, function(wikitext) {
                $.ajax({
                    type: "post",
                    url: "{{ .Url }}",
                    dataType: "json",
                    data: JSON.stringify({
                        host: mw.config.get("wgServerName"),
                        page: mw.config.get("wgPageName"),
                        revid: mw.config.get("wgRevisionId"),
                        pageid: mw.config.get("wgArticleId"),
                        wikitext: wikitext,
                        summary: options.summary,
                    }),
                    success: function() {
                        target.saveDialog.reset();
                        mw.hook("postEdit").fire({ message: "Edit suggested!" });
                        target.deactivate(true);
                    },
                    error: function() {
                        target.showSaveError("Failed to suggest edit.", true, true);
                    },
                });
            });
        };
        ve.init.mw.targetFactory.register(torget);
    });
}());