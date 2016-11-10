(function() {
    mw.libs.ve.targetLoader.addPlugin(function() {
        var torget = function(config) {
            torget.super.call(this, config);
        };
        OO.inheritClass(torget, ve.init.mw.DesktopArticleTarget);
        torget.prototype.save = function(doc, options) {
            var target = ve.init.target;
            target.serialize(doc, function(wikitext) {
                // FIXME: There's gotta be some canonical way of doing this.
                var api = $("link[rel=EditURI]").attr("href");
                api = api.replace(/\?.*$/, "");
                if (/^\/\//.test(api)) {
                    api = location.protocol + api;
                }
                $.ajax({
                    type: "post",
                    url: "{{ .Url }}",
                    dataType: "json",
                    data: JSON.stringify({
                        api: api,
                        wikitext: wikitext,
                        summary: options.summary,
                        revid: mw.config.get("wgRevisionId"),
                        pageid: mw.config.get("wgArticleId"),
                        pagename: mw.config.get("wgPageName"),
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
    mw.hook("postEdit").fire({ message: "Suggestor loaded." });
}());
