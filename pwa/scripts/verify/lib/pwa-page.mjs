function normalizedRoot(rawUrl) {
  const url = new URL(rawUrl);
  url.hash = '';
  url.search = '';
  if (!url.pathname.endsWith('/')) url.pathname += '/';
  return url;
}

export function isExactPwaRoot(pageUrl, pwaUrl) {
  try {
    const page = new URL(pageUrl);
    const root = normalizedRoot(pwaUrl);
    const pagePath = page.pathname.endsWith('/') ? page.pathname : `${page.pathname}/`;
    return page.origin === root.origin
      && pagePath === root.pathname
      && page.search === ''
      && page.hash === '';
  } catch {
    return false;
  }
}

function isPwaCandidate(pageUrl, pwaUrl) {
  try {
    const page = new URL(pageUrl);
    const root = normalizedRoot(pwaUrl);
    return page.origin === root.origin
      && (root.pathname === '/' || page.pathname === root.pathname.slice(0, -1)
        || page.pathname.startsWith(root.pathname));
  } catch {
    return false;
  }
}

export function rankPwaPages(pages, pwaUrl) {
  const debuggablePages = pages.filter((page) => page?.type === 'page' && page.webSocketDebuggerUrl);
  const ranked = [];
  const add = (page) => {
    if (page && !ranked.includes(page)) ranked.push(page);
  };
  debuggablePages.filter((page) => isExactPwaRoot(page.url, pwaUrl)).forEach(add);
  debuggablePages.filter((page) => isPwaCandidate(page.url, pwaUrl)).forEach(add);
  debuggablePages.filter((page) => page.url === 'about:blank').forEach(add);
  debuggablePages.forEach(add);
  return ranked;
}

export function selectPwaPage(pages, pwaUrl, readyPageIds = new Set()) {
  const ranked = rankPwaPages(pages, pwaUrl);
  return ranked.find((page) => readyPageIds.has(page.id)) || ranked[0];
}
