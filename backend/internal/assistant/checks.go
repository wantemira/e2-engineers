package assistant

import (
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func assessChecks(checks domain.Checks, lostCause bool, task string) (string, []string) {
	summary := "Результаты CI недоступны. По анализу diff нельзя утверждать, что тесты прошли."
	switch checks.Status {
	case "PASSED":
		summary = "Получен успешный результат mock CI для проверяемого SHA. Он не доказывает покрытие всех веток."
	case "RUNNING":
		summary = "Mock CI ещё выполняется. Итогового результата тестов пока нет."
	case "FAILED":
		notes := []string{"Mock CI завершился ошибкой."}
		for _, test := range checks.Tests {
			if test.Status != "FAILED" {
				continue
			}
			note := "Не прошёл " + test.Name + "."
			if test.Error != "" {
				note += " Сообщение проверки: " + test.Error + "."
			}
			if lostCause && test.Name == "TestCreateRepositoryError" && strings.Contains(test.Error, "expected wrapped storage error") {
				note += " Проверка ожидает сохранённую причину ошибки; в diff видна новая ошибка без оборачивания исходной. Используйте fmt.Errorf с %w, затем проверьте errors.Is."
			} else {
				note += " Данных недостаточно, чтобы уверенно связать падение с конкретной строкой; сопоставьте лог с изменениями."
			}
			notes = append(notes, note)
		}
		if len(notes) == 1 {
			notes = append(notes, "Отдельные тесты и причина падения не предоставлены.")
		}
		summary = strings.Join(notes, " ")
	}
	suggestions := []string{}
	if lostCause || errorRequirement.MatchString(task) {
		suggestions = append(suggestions, "TestCreateRepositoryError: fake repository возвращает контрольную ошибку; после Create проверьте errors.Is(actual, expected), чтобы сохранить исходную причину.", "TestCreateSuccess: при успешном repository.Create проверьте возвращённый заказ и отсутствие ошибки.")
	}
	return summary, suggestions
}

func checksQuestion(text string) bool {
	return containsAny(text, "тест", "ci", "пайплайн") && containsAny(text, "почему", "объясни", "упал", "падает", "какой тест", "что означает")
}
