// Package render builds the complete public HTML document for the website
// engine. It owns only presentation of an already resolved PageView: routing,
// snapshot resolution, publication projection and prerendering stay in their
// owning components.
//
// The renderer is fail-closed. Every field that can influence markup is
// validated before a template is executed. Visitor- or operator-controlled
// strings (titles, metadata, robots, canonical URLs, attribute values and the
// JSON bootstrap) remain plain strings or JSON and are escaped by
// html/template in their output context. The single exception is ReadyHTML:
// an explicit, validated wrapper for trusted prerender/feature output. Only
// that type is promoted to a trusted HTML value inside this package.
package render
