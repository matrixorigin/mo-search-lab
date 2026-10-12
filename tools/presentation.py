"""Neutral display names for immutable historical benchmark records."""
import re

_legacy = re.compile("anli", re.I)
_markup = re.compile(r"<script\b.*?</script\s*>|<style\b.*?</style\s*>|<[^>]*>", re.I | re.S)
_display_attribute = re.compile(r'\b(?:aria-label|title|alt)="[^"]*"')


def neutral_application_page(page: str) -> str:
    """Keep original lookup keys, scripts and URLs; neutralize visible names."""
    def text(value):
        return _legacy.sub("app", value)

    pieces = []
    offset = 0
    for match in _markup.finditer(page):
        pieces.append(text(page[offset:match.start()]))
        markup = match.group()
        if not markup.lower().startswith(("<script", "<style")):
            markup = _display_attribute.sub(lambda attr: text(attr.group()), markup)
        pieces.append(markup)
        offset = match.end()
    pieces.append(text(page[offset:]))
    return "".join(pieces)
