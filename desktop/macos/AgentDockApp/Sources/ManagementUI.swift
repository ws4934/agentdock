import AppKit

@MainActor enum ManagementUI {
    static func label(_ text: String, size: CGFloat = 13, weight: NSFont.Weight = .regular, secondary: Bool = false) -> NSTextField {
        let field = NSTextField(wrappingLabelWithString: text)
        field.font = .systemFont(ofSize: size, weight: weight)
        field.textColor = secondary ? .secondaryLabelColor : .labelColor
        field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        return field
    }
    static func column(_ views: [NSView], spacing: CGFloat = 12) -> NSStackView {
        let stack = NSStackView(views: views)
        stack.orientation = .vertical; stack.alignment = .leading; stack.spacing = spacing
        for view in views { view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true }
        return stack
    }
    static func card(_ content: NSView) -> NSView {
        let box = NSBox(); box.boxType = .custom; box.titlePosition = .noTitle
        box.cornerRadius = 12; box.borderWidth = 1; box.borderColor = .separatorColor
        box.fillColor = .controlBackgroundColor
        let inner = NSView(); box.contentView = inner
        content.translatesAutoresizingMaskIntoConstraints = false; inner.addSubview(content)
        NSLayoutConstraint.activate([
            content.leadingAnchor.constraint(equalTo: inner.leadingAnchor, constant: 18),
            content.trailingAnchor.constraint(equalTo: inner.trailingAnchor, constant: -18),
            content.topAnchor.constraint(equalTo: inner.topAnchor, constant: 16),
            content.bottomAnchor.constraint(equalTo: inner.bottomAnchor, constant: -16)
        ])
        return box
    }
    static func textScroll(_ text: NSTextView) -> NSScrollView {
        let scroll = NSScrollView(); scroll.hasVerticalScroller = true; scroll.autohidesScrollers = true
        scroll.borderType = .noBorder; scroll.drawsBackground = false
        text.isEditable = false; text.isSelectable = true; text.drawsBackground = false
        text.font = .systemFont(ofSize: 13); text.textColor = .labelColor
        text.textContainerInset = NSSize(width: 18, height: 16)
        text.isVerticallyResizable = true; text.isHorizontallyResizable = false
        text.autoresizingMask = [.width]; text.textContainer?.widthTracksTextView = true
        scroll.documentView = text
        return scroll
    }
}
