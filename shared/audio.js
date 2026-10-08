(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.PartyVisuals = Object.assign(root.PartyVisuals || {}, api);
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';
  const unit = (value) => Number.isFinite(value) ? Math.max(0, Math.min(1, value)) : 0;
  function audioFrame(feed) {
    if (!feed || !Number.isFinite(feed.t) || feed.t < 0 || !feed.party || !feed.spl) return null;
    return {
      t: feed.t,
      eventT: Number.isFinite(feed.spl.eventT) && feed.spl.eventT >= 0 ? feed.spl.eventT : null,
      beat: ['loud', 'soft', 'quiet'].includes(feed.spl.beat) ? feed.spl.beat : null,
      section: ['loud', 'soft', 'quiet'].includes(feed.spl.section) ? feed.spl.section : null,
      energy: unit(feed.spl.level / 100),
      bass: unit(feed.party.bass), mid: unit(feed.party.mid), high: unit(feed.party.high),
    };
  }
  return { audioFrame };
});
