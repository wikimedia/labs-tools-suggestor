/* global $,mw,ve,OO */
let inject = function() {
	const SUGGESTOR_URI = "https://tools.wmflabs.org/suggestor/post";

	let post = function(summary, wikitext) {
		let target = ve.init.target;

		// FIXME: There's gotta be some canonical way of doing this.
		let api = $("link[rel=EditURI]").attr("href");
		api = api.replace(/\?.*$/, "");
		if (/^\/\//.test(api)) {
			api = location.protocol + api;
		}

		$.ajax({
			type: "post",
			url: SUGGESTOR_URI,
			dataType: "json",
			data: JSON.stringify({
				api: api,
				wikitext: wikitext,
				summary: summary,
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
	};

	mw.libs.ve.targetLoader.addPlugin(function() {
		let torget = function(config) {
			torget.super.call(this, config);
		};
		OO.inheritClass(torget, ve.init.mw.DesktopArticleTarget);
		torget.prototype.save = function(doc, options) {
			ve.init.target.serialize(doc, post.bind(null, options.summary));
		};
		ve.init.mw.targetFactory.register(torget);
	});

	mw.hook("postEdit").fire({ message: "Suggestor loaded." });
};

// `content_scripts` can't see variables defined by page scripts, but we want
// to use the mw apis, so let's inject the script in the page.
let script = document.createElement("script");
script.setAttribute("id", "suggestor");
script.appendChild(document.createTextNode(inject.toSource() + "();"));
document.body.appendChild(script);
