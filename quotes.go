package main

import "time"

// Philosophy quotes on focus and getting to work, shown under the title.
// Rotation derives from the wall clock — the existing 10 Hz tick repaints,
// so no extra timer or state is needed.
var quotes = []string{
	"“Concentrate every minute like a Roman on doing what's in front of you.” — Marcus Aurelius",
	"“It is not that we have a short time to live, but that we waste a lot of it.” — Seneca",
	"“While we are postponing, life speeds by.” — Seneca",
	"“How long are you going to wait before you demand the best for yourself?” — Epictetus",
	"“First say to yourself what you would be; then do what you have to do.” — Epictetus",
	"“No great thing is created suddenly.” — Epictetus",
	"“Waste no more time arguing what a good man should be. Be one.” — Marcus Aurelius",
	"“Confine yourself to the present.” — Marcus Aurelius",
	"“Well-being is realized by small steps, but is truly no small thing.” — Zeno",
	"“Beware the barrenness of a busy life.” — Socrates",
	"“The beginning is the most important part of the work.” — Plato",
	"“Pleasure in the job puts perfection in the work.” — Aristotle",
	"“It does not matter how slowly you go as long as you do not stop.” — Confucius",
	"“A journey of a thousand miles begins with a single step.” — Lao Tzu",
	"“The greater the difficulty, the more glory in surmounting it.” — Epicurus",
	"“Begin at once to live, and count each separate day as a separate life.” — Seneca",
}

// quoteFor picks the quote for t's 3-minute window.
func quoteFor(t time.Time) string {
	return quotes[int(t.Unix()/180)%len(quotes)]
}
